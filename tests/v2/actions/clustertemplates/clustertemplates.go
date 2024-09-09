package clustertemplates

import (
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
)

// CreateRkeTemplate takes in the cluster template config parameters and then creates an rke template in rancher which is used for an rke template revision.
func CreateRkeTemplate(client *rancher.Client, members []management.Member) (*management.ClusterTemplate, error) {
	rkeTemplateName := management.ClusterTemplate{
		Name:    namegen.AppendRandomString("rketemplate-"),
		Members: members,
	}

	createTemplate, err := client.Management.ClusterTemplate.Create(&rkeTemplateName)
	return createTemplate, err
}

// CreateRkeTemplateRevision takes in the cluster template revision config parameters and then creates an rke template revision in rancher.
func CreateRkeTemplateRevision(client *rancher.Client, templateRevisionConfig ClusterTemplateRevisionConfig, templateId string) (*management.ClusterTemplateRevision, error) {
	rkeTemplateConfig := NewRKE1ClusterTemplateRevisionTemplate(templateRevisionConfig, templateId)

	clusterTemplateRevision, err := client.Management.ClusterTemplateRevision.Create(&rkeTemplateConfig)
	if err != nil {
		return nil, err
	}
	return clusterTemplateRevision, nil
}

// NewRKE1ClusterTemplateRevisionTemplate is a constructor that creates and returns config required to create cluster template revisions
func NewRKE1ClusterTemplateRevisionTemplate(templateRevisionConfig ClusterTemplateRevisionConfig, templateId string) management.ClusterTemplateRevision {
	var clusterConfig = management.ClusterSpecBase{
		RancherKubernetesEngineConfig: &management.RancherKubernetesEngineConfig{
			Version: templateRevisionConfig.RKE1KubernetesVersion,
			Network: &management.NetworkConfig{
				Plugin: templateRevisionConfig.NetworkPlugin,
			},
			Services: &management.RKEConfigServices{
				Etcd: &management.ETCDService{
					BackupConfig: templateRevisionConfig.BackupConfig,
				},
			},
		},
	}

	var rkeTemplateConfig = management.ClusterTemplateRevision{
		Name:              namegen.AppendRandomString("rketemplate-revision-"),
		ClusterTemplateID: templateId,
		ClusterConfig:     &clusterConfig,
		Questions:         templateRevisionConfig.Questions,
	}

	return rkeTemplateConfig
}

// NewRKE1ClusterTemplateClusterConfig is a constructor that creates and returns an rke1 cluster template config
func NewRKE1ClusterTemplateClusterConfig(clusterName string, client *rancher.Client, clusterTemplateConfig *ClusterTemplateConfig) *management.Cluster {

	newConfig := &management.Cluster{
		DockerRootDir:                 "/var/lib/docker",
		Name:                          clusterName,
		ClusterTemplateID:             clusterTemplateConfig.RKE1TemplateID,
		ClusterTemplateRevisionID:     clusterTemplateConfig.RKE1TemplateRevisionID,
		ClusterTemplateAnswers:        clusterTemplateConfig.RKETemplateAnswers,
		RancherKubernetesEngineConfig: nil,
	}
	return newConfig
}
