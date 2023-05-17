package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/rancher/pkg/capr"
	"github.com/rancher/rancher/pkg/capr/installer"
	"github.com/rancher/rancher/pkg/controllers/capr/etcdmgmt"
	capicontrollers "github.com/rancher/rancher/pkg/generated/controllers/cluster.x-k8s.io/v1beta1"
	rkecontroller "github.com/rancher/rancher/pkg/generated/controllers/rke.cattle.io/v1"
	"github.com/rancher/rancher/pkg/namespace"
	"github.com/rancher/rancher/pkg/serviceaccounttoken"
	"github.com/rancher/rancher/pkg/tls"
	"github.com/rancher/rancher/pkg/wrangler"
	appcontrollers "github.com/rancher/wrangler/pkg/generated/controllers/apps/v1"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	capi "sigs.k8s.io/cluster-api/api/v1beta1"
	capiannotations "sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/secret"
)

const (
	rkeBootstrapName                       = "rke.cattle.io/rkebootstrap-name"
	capiMachinePreTerminateAnnotation      = "pre-terminate.delete.hook.machine.cluster.x-k8s.io/rke-bootstrap-cleanup"
	capiMachinePreTerminateAnnotationOwner = "rke-bootstrap-controller"
)

type handler struct {
	serviceAccountCache corecontrollers.ServiceAccountCache
	secretCache         corecontrollers.SecretCache
	secretClient        corecontrollers.SecretClient
	machineCache        capicontrollers.MachineCache
	machineClient       capicontrollers.MachineClient
	capiClusterCache    capicontrollers.ClusterCache
	deploymentCache     appcontrollers.DeploymentCache
	rkeControlPlanes    rkecontroller.RKEControlPlaneCache
	rkeBootstrap        rkecontroller.RKEBootstrapController
	k8s                 kubernetes.Interface
}

func Register(ctx context.Context, clients *wrangler.Context) {
	h := &handler{
		serviceAccountCache: clients.Core.ServiceAccount().Cache(),
		secretCache:         clients.Core.Secret().Cache(),
		secretClient:        clients.Core.Secret(),
		machineCache:        clients.CAPI.Machine().Cache(),
		machineClient:       clients.CAPI.Machine(),
		capiClusterCache:    clients.CAPI.Cluster().Cache(),
		deploymentCache:     clients.Apps.Deployment().Cache(),
		rkeControlPlanes:    clients.RKE.RKEControlPlane().Cache(),
		rkeBootstrap:        clients.RKE.RKEBootstrap(),
		k8s:                 clients.K8s,
	}

	clients.RKE.RKEBootstrap().OnChange(ctx, "rke-bootstrap-cluster-name", h.OnChange)
	clients.RKE.RKEBootstrap().OnRemove(ctx, "rke-bootstrap-etcd-removal", h.OnRemove)
	rkecontroller.RegisterRKEBootstrapGeneratingHandler(ctx,
		clients.RKE.RKEBootstrap(),
		clients.Apply.
			WithCacheTypes(
				clients.RBAC.Role(),
				clients.RBAC.RoleBinding(),
				clients.CAPI.Machine(),
				clients.Core.ServiceAccount(),
				clients.Core.Secret()).
			WithSetOwnerReference(true, true),
		"",
		"rke-bootstrap",
		h.GeneratingHandler,
		nil)

	relatedresource.Watch(ctx, "rke-bootstrap-trigger", func(namespace, name string, obj runtime.Object) ([]relatedresource.Key, error) {
		if sa, ok := obj.(*corev1.ServiceAccount); ok {
			if name, ok := sa.Labels[rkeBootstrapName]; ok {
				return []relatedresource.Key{
					{
						Namespace: sa.Namespace,
						Name:      name,
					},
				}, nil
			}
		}
		if machine, ok := obj.(*capi.Machine); ok {
			if machine.Spec.Bootstrap.ConfigRef != nil && machine.Spec.Bootstrap.ConfigRef.Kind == "RKEBootstrap" {
				return []relatedresource.Key{{
					Namespace: machine.Namespace,
					Name:      machine.Spec.Bootstrap.ConfigRef.Name,
				}}, nil
			}
		}
		return nil, nil
	}, clients.RKE.RKEBootstrap(), clients.Core.ServiceAccount(), clients.CAPI.Machine())
}

func (h *handler) getBootstrapSecret(namespace, name string, envVars []corev1.EnvVar, machine *capi.Machine) (*corev1.Secret, error) {
	sa, err := h.serviceAccountCache.Get(namespace, name)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}
	secret, err := serviceaccounttoken.EnsureSecretForServiceAccount(context.Background(), h.secretCache.Get, h.k8s, sa)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(secret.Data["token"])

	hasHostPort, err := h.rancherDeploymentHasHostPort()
	if err != nil {
		return nil, err
	}

	is := installer.LinuxInstallScript
	if os := machine.GetLabels()[capr.CattleOSLabel]; os == capr.WindowsMachineOS {
		is = installer.WindowsInstallScript
	}
	data, err := is(context.WithValue(context.Background(), tls.InternalAPI, hasHostPort), base64.URLEncoding.EncodeToString(hash[:]), envVars, "")
	if err != nil {
		return nil, err
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"value": data,
		},
		Type: "rke.cattle.io/bootstrap",
	}, nil
}

func (h *handler) assignPlanSecret(machine *capi.Machine, bootstrap *rkev1.RKEBootstrap) []runtime.Object {
	secretName := capr.PlanSecretFromBootstrapName(bootstrap.Name)
	labels, annotations := getLabelsAndAnnotationsForPlanSecret(bootstrap, machine)

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: bootstrap.Namespace,
			Labels: map[string]string{
				capr.MachineNameLabel: machine.Name,
				rkeBootstrapName:      bootstrap.Name,
				capr.RoleLabel:        capr.RolePlan,
				capr.PlanSecret:       secretName,
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName,
			Namespace:   bootstrap.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Type: capr.SecretTypeMachinePlan,
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: bootstrap.Namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:         []string{"watch", "get", "update", "list"},
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{secretName},
			},
		},
	}
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: bootstrap.Namespace,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.Name,
				Namespace: sa.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     secretName,
		},
	}

	return []runtime.Object{sa, secret, role, roleBinding}
}

func (h *handler) getEnvVar(bootstrap *rkev1.RKEBootstrap, capiCluster *capi.Cluster) ([]corev1.EnvVar, error) {
	if capiCluster.Spec.ControlPlaneRef == nil || capiCluster.Spec.ControlPlaneRef.Kind != "RKEControlPlane" {
		return nil, nil
	}

	cp, err := h.rkeControlPlanes.Get(bootstrap.Namespace, capiCluster.Spec.ControlPlaneRef.Name)
	if err != nil {
		return nil, err
	}

	var result []corev1.EnvVar
	for _, env := range cp.Spec.AgentEnvVars {
		result = append(result, corev1.EnvVar{
			Name:  env.Name,
			Value: env.Value,
		})
	}

	return result, nil
}

func (h *handler) assignBootStrapSecret(machine *capi.Machine, bootstrap *rkev1.RKEBootstrap, capiCluster *capi.Cluster) (*corev1.Secret, []runtime.Object, error) {
	if capi.MachinePhase(machine.Status.Phase) != capi.MachinePhasePending &&
		capi.MachinePhase(machine.Status.Phase) != capi.MachinePhaseDeleting &&
		capi.MachinePhase(machine.Status.Phase) != capi.MachinePhaseFailed &&
		capi.MachinePhase(machine.Status.Phase) != capi.MachinePhaseProvisioning {
		return nil, nil, nil
	}

	envVars, err := h.getEnvVar(bootstrap, capiCluster)
	if err != nil {
		return nil, nil, err
	}

	secretName := name.SafeConcatName(bootstrap.Name, "machine", "bootstrap")

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: bootstrap.Namespace,
			Labels: map[string]string{
				capr.MachineNameLabel: machine.Name,
				rkeBootstrapName:      bootstrap.Name,
				capr.RoleLabel:        capr.RoleBootstrap,
			},
		},
	}

	bootstrapSecret, err := h.getBootstrapSecret(sa.Namespace, sa.Name, envVars, machine)
	if err != nil {
		return nil, nil, err
	}

	return bootstrapSecret, []runtime.Object{sa}, nil
}

func (h *handler) OnChange(_ string, bootstrap *rkev1.RKEBootstrap) (*rkev1.RKEBootstrap, error) {
	if bootstrap == nil {
		return nil, nil
	}

	if !bootstrap.DeletionTimestamp.IsZero() || bootstrap.Spec.ClusterName != "" {
		return bootstrap, nil
	}

	logrus.Debugf("[rkebootstrap] %s/%s: setting cluster name", bootstrap.Namespace, bootstrap.Name)
	// If the bootstrap spec cluster name is blank, we need to update the bootstrap spec to the correct value
	// This is to handle old rkebootstrap objects for unmanaged clusters that did not have the spec properly set
	if v, ok := bootstrap.Labels[capi.ClusterLabelName]; ok && v != "" {
		bootstrap = bootstrap.DeepCopy()
		bootstrap.Spec.ClusterName = v
		return h.rkeBootstrap.Update(bootstrap)
	}

	return bootstrap, nil
}

func (h *handler) GeneratingHandler(bootstrap *rkev1.RKEBootstrap, status rkev1.RKEBootstrapStatus) ([]runtime.Object, rkev1.RKEBootstrapStatus, error) {
	var (
		result []runtime.Object
	)

	machine, err := capr.GetOwnerCAPIMachine(bootstrap, h.machineCache)
	if apierrors.IsNotFound(err) {
		logrus.Debugf("[rkebootstrap] %s/%s: waiting: machine to be set as owner reference", bootstrap.Namespace, bootstrap.Name)
		h.rkeBootstrap.EnqueueAfter(bootstrap.Namespace, bootstrap.Name, 10*time.Second)
		return result, status, generic.ErrSkip
	}
	if err != nil {
		logrus.Errorf("[rkebootstrap] %s/%s: error getting machine by owner reference %v", bootstrap.Namespace, bootstrap.Name, err)
		return nil, status, err
	}

	capiCluster, err := h.capiClusterCache.Get(machine.Namespace, machine.Spec.ClusterName)
	if apierrors.IsNotFound(err) {
		logrus.Debugf("[rkebootstrap] %s/%s: waiting: CAPI cluster does not exist", bootstrap.Namespace, bootstrap.Name)
		h.rkeBootstrap.EnqueueAfter(bootstrap.Namespace, bootstrap.Name, 10*time.Second)
		return result, status, generic.ErrSkip
	}
	if err != nil {
		logrus.Errorf("[rkebootstrap] %s/%s: error getting CAPI cluster %v", bootstrap.Namespace, bootstrap.Name, err)
		return result, status, err
	}

	if capiannotations.IsPaused(capiCluster, bootstrap) {
		logrus.Debugf("[rkebootstrap] %s/%s: waiting: CAPI cluster or RKEBootstrap is paused", bootstrap.Namespace, bootstrap.Name)
		h.rkeBootstrap.EnqueueAfter(bootstrap.Namespace, bootstrap.Name, 10*time.Second)
		return result, status, generic.ErrSkip
	}

	if !capiCluster.Status.InfrastructureReady {
		logrus.Debugf("[rkebootstrap] %s/%s: waiting: CAPI cluster infrastructure is not ready", bootstrap.Namespace, bootstrap.Name)
		h.rkeBootstrap.EnqueueAfter(bootstrap.Namespace, bootstrap.Name, 10*time.Second)
		return result, status, generic.ErrSkip
	}

	result = append(result, h.assignPlanSecret(machine, bootstrap)...)

	_, isEtcd := machine.Labels[capr.EtcdRoleLabel]

	// annotate the CAPI machine with the pre-terminate.delete.hook.machine.cluster.x-k8s.io annotation if it is an etcd machine
	if val, ok := machine.GetAnnotations()[capiMachinePreTerminateAnnotation]; isEtcd && (!ok || val != capiMachinePreTerminateAnnotationOwner) {
		machine = machine.DeepCopy()
		if machine.Labels == nil {
			machine.Labels = make(map[string]string)
		}
		machine.Labels[capiMachinePreTerminateAnnotation] = capiMachinePreTerminateAnnotationOwner
		machine, err = h.machineClient.Update(machine)
		if err != nil {
			return nil, status, err
		}
	}

	bootstrapSecret, objs, err := h.assignBootStrapSecret(machine, bootstrap, capiCluster)
	if err != nil {
		return nil, status, err
	}

	if bootstrapSecret != nil {
		if status.DataSecretName == nil {
			status.DataSecretName = &bootstrapSecret.Name
			status.Ready = true
			logrus.Debugf("[rkebootstrap] %s/%s: setting dataSecretName: %s", bootstrap.Namespace, bootstrap.Name, *status.DataSecretName)
		}
		result = append(result, bootstrapSecret)
	}

	result = append(result, objs...)
	return result, status, nil
}

func (h *handler) rancherDeploymentHasHostPort() (bool, error) {
	deployment, err := h.deploymentCache.Get(namespace.System, "rancher")
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, port := range container.Ports {
			if container.Name == "rancher" && port.HostPort != 0 {
				return true, nil
			}
		}
	}

	return false, nil
}

func getLabelsAndAnnotationsForPlanSecret(bootstrap *rkev1.RKEBootstrap, machine *capi.Machine) (map[string]string, map[string]string) {
	labels := make(map[string]string, len(bootstrap.Labels)+2)
	labels[capr.MachineNameLabel] = machine.Name
	labels[capr.ClusterNameLabel] = bootstrap.Spec.ClusterName
	for k, v := range bootstrap.Labels {
		labels[k] = v
	}

	annotations := make(map[string]string, len(bootstrap.Annotations))
	for k, v := range bootstrap.Annotations {
		annotations[k] = v
	}

	return labels, annotations
}

// OnRemove adds finalizer handling to the RKEBootstrap object, and is used to prevent deletion of the RKE Bootstrap
// when it is deleting and bootstrap is for an etcd node.
func (h *handler) OnRemove(key string, bootstrap *rkev1.RKEBootstrap) (*rkev1.RKEBootstrap, error) {
	logrus.Debugf("[rkebootstrap] %s/%s: OnRemove invoked", bootstrap.Namespace, bootstrap.Name)
	clusterName := bootstrap.Labels[capi.ClusterLabelName]
	if clusterName == "" {
		logrus.Warnf("[rkebootstrap] %s/%s: CAPI cluster label %s was not found in bootstrap labels, allowing bootstrap to delete", bootstrap.Namespace, bootstrap.Name, capi.ClusterLabelName)
		return bootstrap, nil
	}

	capiCluster, err := h.capiClusterCache.Get(bootstrap.Namespace, clusterName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logrus.Warnf("[rkebootstrap] %s/%s: CAPI cluster %s/%s was not found, allowing bootstrap to delete", bootstrap.Namespace, bootstrap.Name, bootstrap.Namespace, clusterName)
			return bootstrap, nil
		}
		return bootstrap, err
	}

	if capiCluster.Spec.ControlPlaneRef == nil {
		logrus.Warnf("[rkebootstrap] %s/%s: CAPI cluster %s/%s controlplane object reference was nil, allowing bootstrap to delete", bootstrap.Namespace, bootstrap.Name, capiCluster.Namespace, capiCluster.Name)
		return bootstrap, nil
	}

	logrus.Debugf("[rkebootstrap] Removing machine %s in cluster %s", key, clusterName)

	cp, err := h.rkeControlPlanes.Get(capiCluster.Spec.ControlPlaneRef.Namespace, capiCluster.Spec.ControlPlaneRef.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logrus.Warnf("[rkebootstrap] %s/%s: RKEControlPlane %s/%s was not found, allowing bootstrap to delete", bootstrap.Namespace, bootstrap.Name, capiCluster.Spec.ControlPlaneRef.Namespace, capiCluster.Spec.ControlPlaneRef.Name)
			return bootstrap, nil
		}
		return bootstrap, err
	}

	machine, err := capr.GetMachineByOwner(h.machineCache, bootstrap)
	if err != nil {
		if errors.Is(err, capr.ErrNoMachineOwnerRef) || apierrors.IsNotFound(err) {
			// If we did not find the machine by owner ref or the cache returned a not found, then proceed with deletion
			return bootstrap, nil
		}
		return bootstrap, err
	}

	// The controlplane is owned by the capi cluster and will not be deleted until the capi cluster is deleted.
	if cp.DeletionTimestamp != nil || capiCluster.DeletionTimestamp != nil {
		return h.removeMachinePreTerminateAnnotation(bootstrap, machine)
	}

	if _, ok := machine.Labels[capr.EtcdRoleLabel]; !ok {
		logrus.Debugf("[rkebootstrap] Safe removal for machine %s in cluster %s not necessary as it is not an etcd node", key, clusterName)
		return h.removeMachinePreTerminateAnnotation(bootstrap, machine) // If we are not dealing with an etcd node, we can go ahead and allow removal
	}

	if v, ok := bootstrap.Annotations[capr.ForceRemoveEtcdAnnotation]; ok && strings.ToLower(v) == "true" {
		logrus.Infof("[rkebootstrap] Force removing etcd machine %s in cluster %s", key, clusterName)
		return h.removeMachinePreTerminateAnnotation(bootstrap, machine)
	}

	if machine.Status.NodeRef == nil {
		logrus.Infof("[rkebootstrap] No associated node found for machine %s in cluster %s, proceeding with removal", key, clusterName)
		return h.removeMachinePreTerminateAnnotation(bootstrap, machine)
	}

	kcSecret, err := h.secretCache.Get(bootstrap.Namespace, secret.Name(clusterName, secret.Kubeconfig))
	if err != nil {
		if apierrors.IsNotFound(err) {
			return h.removeMachinePreTerminateAnnotation(bootstrap, machine)
		}
		return bootstrap, err
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kcSecret.Data["value"])
	if err != nil {
		return bootstrap, err
	}

	removed, err := etcdmgmt.SafelyRemoved(restConfig, capr.GetRuntimeCommand(cp.Spec.KubernetesVersion), machine.Status.NodeRef.Name)
	if err != nil {
		return bootstrap, err
	}
	if !removed {
		h.rkeBootstrap.EnqueueAfter(bootstrap.Namespace, bootstrap.Name, 5*time.Second)
		return bootstrap, generic.ErrSkip
	}
	return h.removeMachinePreTerminateAnnotation(bootstrap, machine)
}

// removeMachinePreTerminateAnnotation removes the pre-terminate annotation from a CAPI machine when we removing the rkebootstrap, indicating the infrastructure can be deleted.
func (h *handler) removeMachinePreTerminateAnnotation(bootstrap *rkev1.RKEBootstrap, machine *capi.Machine) (*rkev1.RKEBootstrap, error) {
	if machine == nil || machine.Annotations == nil {
		return bootstrap, nil
	}

	var err error
	if _, ok := machine.GetAnnotations()[capiMachinePreTerminateAnnotation]; ok {
		machine = machine.DeepCopy()
		delete(machine.Annotations, capiMachinePreTerminateAnnotation)
		_, err = h.machineClient.Update(machine)
	}
	return bootstrap, err
}
