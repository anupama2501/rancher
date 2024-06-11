package configmaps

import (
	"github.com/rancher/shepherd/clients/rancher"

	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/namespaces"
	"github.com/rancher/shepherd/extensions/projects"
	"github.com/rancher/shepherd/extensions/workloads"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/wrangler"
	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	deploymentName = namegen.AppendRandomString("cm-rbac-")
	configMap      = &coreV1.ConfigMap{
		ObjectMeta: metaV1.ObjectMeta{
			Name:      namegen.AppendRandomString("test-cm"),
			Namespace: "",
		},
		Data: map[string]string{
			"foo": "bar",
		},
	}
)

const (
	containerImage = "nginx"
)

func newPodTemplate(envVarObject, volumeObject string) (podTemplate coreV1.PodTemplateSpec) {
	containerName := namegen.AppendRandomString("cm-con")
	containerTemplate := workloads.NewContainer(containerName, containerImage, coreV1.PullAlways, []coreV1.VolumeMount{}, []coreV1.EnvFromSource{}, nil, nil, nil)

	if envVarObject != "" {
		containerTemplate.EnvFrom = []coreV1.EnvFromSource{
			{
				ConfigMapRef: &coreV1.ConfigMapEnvSource{
					LocalObjectReference: coreV1.LocalObjectReference{Name: envVarObject},
				},
			},
		}
	}
	podTemplate = workloads.NewPodTemplate([]coreV1.Container{containerTemplate}, []coreV1.Volume{}, []coreV1.LocalObjectReference{}, nil)
	if volumeObject != "" {
		podTemplate.Spec.Volumes = []coreV1.Volume{
			{
				Name: namegen.AppendRandomString("cm-"),
				VolumeSource: coreV1.VolumeSource{
					ConfigMap: &coreV1.ConfigMapVolumeSource{
						LocalObjectReference: coreV1.LocalObjectReference{Name: volumeObject},
					},
				},
			},
		}
	}

	return podTemplate
}

func createConfigmap(namespace *v1.SteveAPIObject, wranglerContext *wrangler.Context) (*coreV1.ConfigMap, error) {
	configMap.Namespace = namespace.Name
	configMap, err := wranglerContext.Core.ConfigMap().Create(configMap)

	return configMap, err
}

func createProjectAndNamespace(client *rancher.Client, clusterID string) (*management.Project, *v1.SteveAPIObject, error) {
	project, err := client.Management.Project.Create(projects.NewProjectConfig(clusterID))
	if err != nil {
		return nil, nil, err
	}
	namespaceName := namegen.AppendRandomString("testns-")
	namespace, err := namespaces.CreateNamespace(client, namespaceName, "{}", map[string]string{}, map[string]string{}, project)
	if err != nil {
		return nil, nil, err
	}
	return project, namespace, nil
}
