package rbac

import (
	"time"

	"github.com/rancher/norman/types/slice"
	clusterv2 "github.com/rancher/rancher/pkg/controllers/provisioningv2/cluster"
	provisioningcontrollers "github.com/rancher/rancher/pkg/generated/controllers/provisioning.cattle.io/v1"
	v3 "github.com/rancher/rancher/pkg/generated/norman/management.cattle.io/v3"
	rbacv1 "github.com/rancher/rancher/pkg/generated/norman/rbac.authorization.k8s.io/v1"
	"github.com/rancher/rancher/pkg/rbac"
	"github.com/rancher/rancher/pkg/types/config"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/sirupsen/logrus"
	k8srbac "k8s.io/api/rbac/v1"
	v12 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

const (
	grbByUserAndRoleIndex = "authz.cluster.cattle.io/grb-by-user-and-role"
)

func RegisterIndexers(scaledContext *config.ScaledContext) error {
	informer := scaledContext.Management.GlobalRoleBindings("").Controller().Informer()
	indexers := map[string]cache.IndexFunc{
		grbByUserAndRoleIndex: grbByUserAndRole,
		grbByRoleIndex:        grbByRole,
	}
	if err := informer.AddIndexers(indexers); err != nil {
		return err
	}

	// Add cache informer to project role template bindings
	prtbInformer := scaledContext.Management.ProjectRoleTemplateBindings("").Controller().Informer()
	prtbIndexers := map[string]cache.IndexFunc{
		prtbByProjectIndex:               prtbByProjectName,
		prtbByProjecSubjectIndex:         prtbByProjectAndSubject,
		rtbByClusterAndRoleTemplateIndex: rtbByClusterAndRoleTemplateName,
		prtbByUIDIndex:                   prtbByUID,
		prtbByNsAndNameIndex:             prtbByNsName,
		rtbByClusterAndUserIndex:         rtbByClusterAndUserNotDeleting,
	}
	if err := prtbInformer.AddIndexers(prtbIndexers); err != nil {
		return err
	}

	crtbInformer := scaledContext.Management.ClusterRoleTemplateBindings("").Controller().Informer()
	crtbIndexers := map[string]cache.IndexFunc{
		rtbByClusterAndRoleTemplateIndex: rtbByClusterAndRoleTemplateName,
		rtbByClusterAndUserIndex:         rtbByClusterAndUserNotDeleting,
	}
	return crtbInformer.AddIndexers(crtbIndexers)
}

func newGlobalRoleBindingHandler(workload *config.UserContext) v3.GlobalRoleBindingHandlerFunc {
	informer := workload.Management.Management.GlobalRoleBindings("").Controller().Informer()

	h := &grbHandler{
		clusterName:         workload.ClusterName,
		grbIndexer:          informer.GetIndexer(),
		clusterRoleBindings: workload.RBAC.ClusterRoleBindings(""),
		crbLister:           workload.RBAC.ClusterRoleBindings("").Controller().Lister(),
		// The following clients/controllers all point at the management cluster
		grLister:                    workload.Management.Management.GlobalRoles("").Controller().Lister(),
		rbLister:                    workload.Management.RBAC.RoleBindings("").Controller().Lister(),
		roleBindings:                workload.Management.RBAC.RoleBindings(""),
		globalroleBindingController: workload.Management.Management.GlobalRoleBindings("").Controller(),
		clusters:                    workload.Management.Management.Clusters(""),
		provClusters:                workload.Management.Wrangler.Provisioning.Cluster().Cache(),
	}

	return h.sync
}

// grbHandler ensures the global admins have full access to every cluster. If a globalRoleBinding is created that uses
// the admin role, then the user in that binding gets a clusterRoleBinding in every user cluster to the cluster-admin role
type grbHandler struct {
	clusterName                 string
	clusterRoleBindings         rbacv1.ClusterRoleBindingInterface
	crbLister                   rbacv1.ClusterRoleBindingLister
	grbIndexer                  cache.Indexer
	grLister                    v3.GlobalRoleLister
	rbLister                    rbacv1.RoleBindingLister
	roleBindings                rbacv1.RoleBindingInterface
	globalroleBindingController v3.GlobalRoleBindingController
	clusters                    v3.ClusterInterface
	provClusters                provisioningcontrollers.ClusterCache
}

func (c *grbHandler) sync(key string, obj *v3.GlobalRoleBinding) (runtime.Object, error) {
	if obj == nil || obj.DeletionTimestamp != nil {
		return obj, nil
	}

	isAdmin, err := c.isAdminRole(obj.GlobalRoleName)
	if err != nil {
		return nil, err
	} else if !isAdmin {
		return obj, nil
	}

	// Do not sync restricted-admin to the local cluster as 'cluster-admin'
	if c.clusterName == "local" && obj.GlobalRoleName == rbac.GlobalRestrictedAdmin {
		return obj, nil
	}

	logrus.Debugf("%v is an admin role", obj.GlobalRoleName)

	err = c.ensureClusterAdminBinding(obj)
	if err != nil {
		return nil, err
	}

	err = c.ensureProvisioningClusterAdminBinding(obj)
	if err != nil {
		return nil, err
	}

	return obj, nil
}

// ensureClusterAdminBinding creates a cluster-admin binding in the downstream cluster
func (c *grbHandler) ensureClusterAdminBinding(obj *v3.GlobalRoleBinding) error {
	bindingName := rbac.GrbCRBName(obj)
	b, err := c.crbLister.Get("", bindingName)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if b != nil {
		// binding exists, nothing to do
		return nil
	}

	_, err = c.clusterRoleBindings.Create(&v12.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: bindingName,
		},
		Subjects: []v12.Subject{rbac.GetGRBSubject(obj)},
		RoleRef: v12.RoleRef{
			Name: "cluster-admin",
			Kind: "ClusterRole",
		},
	})
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}
	return nil
}

// ensureProvisioningClusterAdminBinding creates a bindings from the restricted-admin to the
// cluster-owner role in the cluster namespace in the management cluster. This grants
// permissions to all the v2 provisioning resources that are marked as cluster indexed in the
// management cluster.
func (c *grbHandler) ensureProvisioningClusterAdminBinding(obj *v3.GlobalRoleBinding) error {
	// Restricted-admin needs this, a regular admin will already have access to all the resources
	// this binding grants in the management cluster.
	if obj.GlobalRoleName != rbac.GlobalRestrictedAdmin {
		return nil
	}

	pClusters, err := c.provClusters.GetByIndex(clusterv2.ByCluster, c.clusterName)
	if err != nil {
		return err
	}

	if len(pClusters) == 0 {
		// When no provisioning cluster is found, enqueue the GRB to wait for
		// the provisioning cluster to be created.
		logrus.Debugf("No provisioning cluster found for cluster %v in GRB sync, enqueuing", c.clusterName)
		c.globalroleBindingController.EnqueueAfter(obj.Namespace, obj.Name, 10*time.Second)
		return nil
	}

	provCluster := pClusters[0]

	subject := rbac.GetGRBSubject(obj)
	rbName := name.SafeConcatName(rbac.ProvisioningClusterAdminName(provCluster), rbac.GetGRBTargetKey(obj))

	existingRb, err := c.rbLister.Get(provCluster.Namespace, rbName)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if existingRb != nil {
		return nil
	}

	_, err = c.roleBindings.Create(&k8srbac.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbName,
			Namespace: provCluster.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: obj.APIVersion,
					Kind:       obj.Kind,
					Name:       obj.Name,
					UID:        obj.UID,
				},
			},
		},
		RoleRef: k8srbac.RoleRef{
			APIGroup: k8srbac.GroupName,
			Kind:     "Role",
			Name:     rbac.ProvisioningClusterAdminName(provCluster),
		},
		Subjects: []k8srbac.Subject{subject},
	})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

// isAdminRole detects whether a GlobalRole has admin permissions or not.
func (c *grbHandler) isAdminRole(rtName string) (bool, error) {
	gr, err := c.grLister.Get("", rtName)
	if err != nil {
		return false, err
	}

	// global role is builtin admin role
	if gr.Builtin && (gr.Name == rbac.GlobalAdmin || gr.Name == rbac.GlobalRestrictedAdmin) {
		return true, nil
	}

	var hasResourceRule, hasNonResourceRule bool
	for _, rule := range gr.Rules {
		if slice.ContainsString(rule.Resources, "*") && slice.ContainsString(rule.APIGroups, "*") && slice.ContainsString(rule.Verbs, "*") {
			hasResourceRule = true
			continue
		}
		if slice.ContainsString(rule.NonResourceURLs, "*") && slice.ContainsString(rule.Verbs, "*") {
			hasNonResourceRule = true
			continue
		}
	}

	// global role has an admin resource rule, and admin nonResourceURLs rule
	if hasResourceRule && hasNonResourceRule {
		return true, nil
	}

	return false, nil
}

func grbByUserAndRole(obj interface{}) ([]string, error) {
	grb, ok := obj.(*v3.GlobalRoleBinding)
	if !ok {
		return []string{}, nil
	}

	return []string{rbac.GetGRBTargetKey(grb) + "-" + grb.GlobalRoleName}, nil
}
