//go:build (validation || sanity) && !infra.any && !infra.aks && !infra.eks && !infra.rke2k3s && !infra.gke && !infra.rke1 && !cluster.any && !cluster.custom && !cluster.nodedriver && !extended && !stress

package rke1

import (
	"testing"

	"github.com/rancher/rancher/tests/v2/actions/clusters"
	"github.com/rancher/rancher/tests/v2/actions/clustertemplates"
	rke1 "github.com/rancher/rancher/tests/v2/actions/clustertemplates"
	"github.com/rancher/rancher/tests/v2/actions/provisioning"
	"github.com/rancher/rancher/tests/v2/actions/provisioninginput"
	"github.com/rancher/shepherd/clients/rancher"
	mgmt "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/extensions/settings"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	extensionsetcdsnapshot "github.com/rancher/shepherd/extensions/etcdsnapshot"
	"github.com/rancher/rancher/tests/v2/actions/etcdsnapshot"
)

const (
	cniCalico                        = "calico"
	clusterEnforcementSetting        = "cluster-template-enforcement"
	enabledClusterEnforcementSetting = "true"
	isRequiredQuestion               = true
	userPrincipalID                  = "local://"
)

var (
	Questions = []mgmt.Question{
		0: {
			Variable: "rancherKubernetesEngineConfig.kubernetesVersion",
			Default:  "",
			Required: isRequiredQuestion,
			Type:     "string",
		},
		1: {
			Variable: "rancherKubernetesEngineConfig.network.plugin",
			Default:  "canal",
			Required: isRequiredQuestion,
			Type:     "string",
		},
		2: {
			Variable: "rancherKubernetesEngineConfig.services.etcd.backupConfig.s3BackupConfig.bucketName",
			Required: isRequiredQuestion,
			Default:  "",
			Type:     "string",
		},
		3: {
			Variable: "rancherKubernetesEngineConfig.services.etcd.backupConfig.s3BackupConfig.accessKey",
			Required: isRequiredQuestion,
			Default:  "",
			Type:     "string",
		},
		4: {
			Variable: "rancherKubernetesEngineConfig.services.etcd.backupConfig.s3BackupConfig.secretKey",
			Required: isRequiredQuestion,
			Default:  "",
			Type:     "string",
		},
		5: {
			Variable: "rancherKubernetesEngineConfig.services.etcd.backupConfig.s3BackupConfig.endpoint",
			Required: isRequiredQuestion,
			Default:  "",
			Type:     "string",
		},
		6: {
			Variable: "rancherKubernetesEngineConfig.services.etcd.backupConfig.s3BackupConfig.region",
			Required: isRequiredQuestion,
			Default:  "",
			Type:     "string",
		},
	}
)

type ClusterTemplateProvisioningTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	provisioningConfig *provisioninginput.Config
}

func (c *ClusterTemplateProvisioningTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *ClusterTemplateProvisioningTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	c.provisioningConfig = new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, c.provisioningConfig)

	client, err := rancher.NewClient("", testSession) //c.client
	require.NoError(c.T(), err)

	c.client = client

	if c.provisioningConfig.RKE1KubernetesVersions == nil {
		rke1Versions, err := kubernetesversions.ListRKE1AllVersions(c.client)
		require.NoError(c.T(), err)

		c.provisioningConfig.RKE1KubernetesVersions = []string{rke1Versions[len(rke1Versions)-1]}
	}

	if c.provisioningConfig.CNIs == nil {
		c.provisioningConfig.CNIs = []string{cniCalico}
	}

}

func (c *ClusterTemplateProvisioningTestSuite) createRke1ClusterTemplateAndTemplateRevisions(clusterTemplateRevisionConfig clustertemplates.ClusterTemplateRevisionConfig, answers map[string]string, members []mgmt.Member) *clustertemplates.ClusterTemplateConfig {
	clusterTemplate, err := rke1.CreateRkeTemplate(c.client, members)
	require.NoError(c.T(), err)

	clusterTemplateRevision, err := rke1.CreateRkeTemplateRevision(c.client, clusterTemplateRevisionConfig, clusterTemplate.ID)
	require.NoError(c.T(), err)

	clusterTemplateClusterConfig := new(clustertemplates.ClusterTemplateConfig)
	clusterTemplateClusterConfig.RKE1TemplateID = clusterTemplate.ID
	clusterTemplateClusterConfig.RKE1TemplateRevisionID = clusterTemplateRevision.ID

	if answers != nil {
		clusterTemplateClusterConfig.RKETemplateAnswers = &mgmt.Answer{
			Values: answers,
		}
	}
	return clusterTemplateClusterConfig
}


func (c *ClusterTemplateProvisioningTestSuite) testCreateRke1ClusterWithTemplateAndVerify(client *rancher.Client, clusterTempClusterConfig *rke1.ClusterTemplateConfig) *mgmt.Cluster {

	rke1Provider := provisioning.CreateRKE1Provider(c.provisioningConfig.NodeProviders[0])
	nodeTemplate, err := rke1Provider.NodeTemplateFunc(client)
	require.NoError(c.T(), err)

	clusterConfig := clusters.ConvertConfigToClusterConfig(c.provisioningConfig)

	nodeAndRoles := []provisioninginput.NodePools{
		provisioninginput.AllRolesNodePool,
	}
	clusterConfig.NodePools = nodeAndRoles

	clusterObject, err := provisioning.CreateProvisioningRKE1ClusterWithClusterTemplate(client, rke1Provider, clusterTempClusterConfig, clusterConfig, nodeTemplate)
	require.NoError(c.T(), err)

	clusterConfig.KubernetesVersion = c.provisioningConfig.RKE1KubernetesVersions[0]
	provisioning.VerifyRKE1Cluster(c.T(), c.client, clusterConfig, clusterObject)

	if clusterObject.RancherKubernetesEngineConfig.Services.Etcd.BackupConfig.S3BackupConfig != nil{
		err := extensionsetcdsnapshot.CreateRKE1Snapshot(client, clusterObject.Name)
		require.NoError(c.T(), err)

		existingSnapshots, err := extensionsetcdsnapshot.GetRKE1Snapshots(client, clusterObject.ID)
		etcdNodeCount, _ := etcdsnapshot.MatchNodeToAnyEtcdRole(client, clusterObject.ID)

		_, err = provisioning.VerifySnapshots(client, clusterObject.Name, etcdNodeCount+len(existingSnapshots), true)
		require.NoError(c.T(), err)
	}

	return clusterObject
}

func (c *ClusterTemplateProvisioningTestSuite) TestProvisioningRKE1ClusterWithClusterTemplate() {
	log.Info("Create an rke template and creating a downstream node driver with the rke template.")

	templateRevisionConfig := new(clustertemplates.ClusterTemplateRevisionConfig)
	templateRevisionConfig.NetworkPlugin = c.provisioningConfig.CNIs[0]
	templateRevisionConfig.RKE1KubernetesVersion = c.provisioningConfig.RKE1KubernetesVersions[0]

	if c.provisioningConfig.ETCDRKE1 != nil {
		templateRevisionConfig.BackupConfig = c.provisioningConfig.ETCDRKE1.BackupConfig
	}

	clusterConfig := c.createRke1ClusterTemplateAndTemplateRevisions(*templateRevisionConfig, nil, nil)

	c.testCreateRke1ClusterWithTemplateAndVerify(c.client, clusterConfig)
}

func (c *ClusterTemplateProvisioningTestSuite) TestEnforceClusterTemplateProvisioningRKE1Cluster() {
	log.Info("Enforcing cluster template while provisioning rke1 clusters")

	steveAdminClient := c.client.Steve

	clusterEnforcement, err := steveAdminClient.SteveType(settings.ManagementSetting).ByID(clusterEnforcementSetting)
	require.NoError(c.T(), err)

	_, err = settings.UpdateGlobalSettings(steveAdminClient, clusterEnforcement, enabledClusterEnforcementSetting)
	require.NoError(c.T(), err)

	verifySetting, err := steveAdminClient.SteveType(settings.ManagementSetting).ByID(clusterEnforcementSetting)
	require.NoError(c.T(), err)
	require.Equal(c.T(), verifySetting.JSONResp["value"], enabledClusterEnforcementSetting)

	log.Info("Create a standard user and add them to the rke template.")
	user, err := users.CreateUserWithRole(c.client, users.UserConfig(), "user")
	require.NoError(c.T(), err)
	standardClient, err := c.client.AsUser(user)
	require.NoError(c.T(), err)

	templateRevisionConfig := new(clustertemplates.ClusterTemplateRevisionConfig)
	templateRevisionConfig.Name = namegenerator.AppendRandomString("rev1")
	templateRevisionConfig.NetworkPlugin = c.provisioningConfig.CNIs[0]
	templateRevisionConfig.RKE1KubernetesVersion = c.provisioningConfig.RKE1KubernetesVersions[0]
	members := []mgmt.Member{
		0: {AccessType: "owner",
			UserPrincipalID: userPrincipalID + user.ID},
	}

	if c.provisioningConfig.ETCDRKE1 != nil {
		templateRevisionConfig.BackupConfig = c.provisioningConfig.ETCDRKE1.BackupConfig
	}

	log.Info("Create a downstream cluster as the standard user.")
	clusterConfig := c.createRke1ClusterTemplateAndTemplateRevisions(*templateRevisionConfig, nil, members)
	c.testCreateRke1ClusterWithTemplateAndVerify(standardClient, clusterConfig)

}

func (c *ClusterTemplateProvisioningTestSuite) TestClusterTemplateWithQuestionsProvisioningRKE1Cluster() {
	log.Info("Creating an rke template with questions")

	templateRevisionConfig := new(clustertemplates.ClusterTemplateRevisionConfig)

	templateRevisionConfig.Questions = Questions
	s3EtcdEnabled := true
	templateRevisionConfig.BackupConfig = &mgmt.BackupConfig{
		Enabled:        &s3EtcdEnabled,
		S3BackupConfig: &mgmt.S3BackupConfig{},
	}

	answers := map[string]string{
		"rancherKubernetesEngineConfig.kubernetesVersion":                                    c.provisioningConfig.RKE1KubernetesVersions[0],
		"rancherKubernetesEngineConfig.network.plugin":                                       c.provisioningConfig.CNIs[0],
		"rancherKubernetesEngineConfig.services.etcd.backupConfig.s3BackupConfig.accessKey":  c.provisioningConfig.ETCDRKE1.BackupConfig.S3BackupConfig.AccessKey,
		"rancherKubernetesEngineConfig.services.etcd.backupConfig.s3BackupConfig.bucketName": c.provisioningConfig.ETCDRKE1.BackupConfig.S3BackupConfig.BucketName,
		"rancherKubernetesEngineConfig.services.etcd.backupConfig.s3BackupConfig.endpoint":   c.provisioningConfig.ETCDRKE1.BackupConfig.S3BackupConfig.Endpoint,
		"rancherKubernetesEngineConfig.services.etcd.backupConfig.s3BackupConfig.region":     c.provisioningConfig.ETCDRKE1.BackupConfig.S3BackupConfig.Region,
		"rancherKubernetesEngineConfig.services.etcd.backupConfig.s3BackupConfig.secretKey":  c.provisioningConfig.ETCDRKE1.BackupConfig.S3BackupConfig.SecretKey,
	}

	clusterConfig := c.createRke1ClusterTemplateAndTemplateRevisions(*templateRevisionConfig, answers, nil)

	c.testCreateRke1ClusterWithTemplateAndVerify(c.client, clusterConfig)
}

func (c *ClusterTemplateProvisioningTestSuite) TestClusterTemplateEditAsAdmin() {
	log.Info("Creating an rke template with two revisions")
	templateRevisionConfig := new(clustertemplates.ClusterTemplateRevisionConfig)

	rke1Versions, err := kubernetesversions.ListRKE1AllVersions(c.client)
	require.NoError(c.T(), err)
	templateRevisionConfig.Name = namegenerator.AppendRandomString("rev1")
	templateRevisionConfig.NetworkPlugin = c.provisioningConfig.CNIs[0]
	templateRevisionConfig.RKE1KubernetesVersion = rke1Versions[len(rke1Versions)-2]

	if c.provisioningConfig.ETCDRKE1 != nil {
		templateRevisionConfig.BackupConfig = c.provisioningConfig.ETCDRKE1.BackupConfig
	}

	log.Info("Creating rke1 template and rke template revision1")
	templateRevision1 := c.createRke1ClusterTemplateAndTemplateRevisions(*templateRevisionConfig, nil, nil)
	templateRevisionConfig.RKE1KubernetesVersion = rke1Versions[len(rke1Versions)-1]

	log.Info("Creating a new rke template revisions in the previously created template")

	templateRevision2, err := rke1.CreateRkeTemplateRevision(c.client, *templateRevisionConfig, templateRevision1.RKE1TemplateID)
	require.NoError(c.T(), err)

	log.Info("Create an rke1 cluster with template revision1")
	clusterObj := c.testCreateRke1ClusterWithTemplateAndVerify(c.client, templateRevision1)

	templateRevision1.RKE1TemplateRevisionID = templateRevision2.ID

	log.Info("Update the rke1 cluster with template revision 2")
	revisedCluster := clustertemplates.NewRKE1ClusterTemplateClusterConfig(clusterObj.Name, c.client, templateRevision1)
	_, err = extensionscluster.UpdateRKE1Cluster(c.client, clusterObj, revisedCluster)
	require.NoError(c.T(), err)

	require.Equal(c.T(), clusterObj.Version, templateRevisionConfig.RKE1KubernetesVersion)
}

func (c *ClusterTemplateProvisioningTestSuite) TestExportClusterTemplate() {
	log.Info("Creating an rke1 cluster")

	rke1Provider := provisioning.CreateRKE1Provider(c.provisioningConfig.NodeProviders[0])
	nodeTemplate, err := rke1Provider.NodeTemplateFunc(c.client)
	require.NoError(c.T(), err)

	clusterConfig := clusters.ConvertConfigToClusterConfig(c.provisioningConfig)

	nodeAndRoles := []provisioninginput.NodePools{
		provisioninginput.AllRolesNodePool,
	}
	clusterConfig.NodePools = nodeAndRoles

	clusterConfig.CNI = c.provisioningConfig.CNIs[0]
	clusterConfig.KubernetesVersion  = c.provisioningConfig.RKE1KubernetesVersions[0]

	clusterObject, err := provisioning.CreateProvisioningRKE1Cluster(c.client, rke1Provider, clusterConfig, nodeTemplate)
	require.NoError(c.T(), err)

	provisioning.VerifyRKE1Cluster(c.T(), c.client, clusterConfig, clusterObject)

	log.Info("Exporting the newly cluster after its provisioned as a cluster template.")
	rkeTemplateExport, err := c.client.Management.Cluster.ActionSaveAsTemplate(clusterObject, 
		&mgmt.SaveAsTemplateInput{ClusterTemplateName: namegenerator.AppendRandomString("template"),
		ClusterTemplateRevisionName: namegenerator.AppendRandomString("revision")})
	require.NoError(c.T(), err)

	template, err := c.client.Management.ClusterTemplateRevision.ByID(rkeTemplateExport.ClusterTemplateRevisionName)
	require.NoError(c.T(), err)

	require.Equal(c.T(), template.ClusterConfig.RancherKubernetesEngineConfig.Version, clusterObject.Version)
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestClusterTemplateRKE1ProvisioningTestSuite(t *testing.T) {
	suite.Run(t, new(ClusterTemplateProvisioningTestSuite))
}
