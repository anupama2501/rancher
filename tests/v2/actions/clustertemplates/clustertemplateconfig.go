package clustertemplates

import (
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
)

type ClusterTemplateConfig struct {
	RKE1TemplateID         string             `json:"clusterTemplateId" yaml:"clusterTemplateId"`
	RKE1TemplateRevisionID string             `json:"clusterTemplateRevisionId" yaml:"clusterTemplateRevisionId"`
	RKETemplateAnswers     *management.Answer `json:"answers,omitempty" yaml:"answers,omitempty"`
}

type ClusterTemplateRevisionConfig struct {
	Name                  string                   `json:"name,omitempty" yaml:"name,omitempty"`
	RKE1KubernetesVersion string                   `json:"rke1kubeversion,omitempty" yaml:"rke1kubeversion,omitempty"`
	BackupConfig          *management.BackupConfig `json:"backupConfig,omitempty" yaml:"backupConfig,omitempty"`
	NetworkPlugin         string                   `json:"plugin,omitempty" yaml:"plugin,omitempty"`
	Questions             []management.Question    `json:"questions,omitempty" yaml:"questions,omitempty"`
}
