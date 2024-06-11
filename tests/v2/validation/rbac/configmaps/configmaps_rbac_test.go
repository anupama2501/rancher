//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package configmaps

import (
	"testing"

	"github.com/rancher/rancher/tests/v2/actions/rbac"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	dep "github.com/rancher/shepherd/extensions/kubeapi/workloads/deployments"
	"github.com/rancher/shepherd/pkg/session"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8sError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ConfigmapsRBACTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (cm *ConfigmapsRBACTestSuite) TearDownSuite() {
	cm.session.Cleanup()
}

func (cm *ConfigmapsRBACTestSuite) SetupSuite() {
	cm.session = session.NewSession()

	client, err := rancher.NewClient("", cm.session)
	require.NoError(cm.T(), err)

	cm.client = client

	log.Info("Getting cluster name from the config file and append cluster details in cm")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(cm.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(cm.client, clusterName)
	require.NoError(cm.T(), err, "Error getting cluster ID")
	cm.cluster, err = cm.client.Management.Cluster.ByID(clusterID)
	require.NoError(cm.T(), err)
}

func (cm *ConfigmapsRBACTestSuite) TestCreateConfigmapAsVolume() {
	subSession := cm.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		cm.Run("Validate config map creation for user with role "+tt.role.String(), func() {
			adminProject, namespace, err := createProjectAndNamespace(cm.client, cm.cluster.ID)
			require.NoError(cm.T(), err)

			newUser, standardUserClient, err := rbac.CreateUserAndAddToClusterRole(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			require.NoError(cm.T(), err)
			cm.T().Logf("Created user: %v", newUser.Username)

			downstreamWranglerContext, err := standardUserClient.WranglerContext.DownStreamClusterWranglerContext(cm.cluster.ID)
			require.NoError(cm.T(), err)

			configMapCreatedByUser, err := createConfigmap(namespace, downstreamWranglerContext)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				require.NoError(cm.T(), err)
				_, err = dep.CreateDeployment(standardUserClient, cm.cluster.ID, deploymentName, namespace.Name, newPodTemplate("", configMapCreatedByUser.Name), 1)
				require.NoError(cm.T(), err)
			case rbac.ClusterMember.String():
				require.Error(cm.T(), err)
				require.True(cm.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func (cm *ConfigmapsRBACTestSuite) TestCreateConfigmapAsEnvVar() {
	subSession := cm.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}
	for _, tt := range tests {
		cm.Run("Validate config map creation for user with role "+tt.role.String(), func() {
			adminProject, namespace, err := createProjectAndNamespace(cm.client, cm.cluster.ID)
			require.NoError(cm.T(), err)

			newUser, standardUserClient, err := rbac.CreateUserAndAddToClusterRole(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			require.NoError(cm.T(), err)
			cm.T().Logf("Created user: %v", newUser.Username)

			downstreamWranglerContext, err := standardUserClient.WranglerContext.DownStreamClusterWranglerContext(cm.cluster.ID)
			require.NoError(cm.T(), err)

			configMapCreatedByUser, err := createConfigmap(namespace, downstreamWranglerContext)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				require.NoError(cm.T(), err)
				_, err = dep.CreateDeployment(standardUserClient, cm.cluster.ID, deploymentName, namespace.Name, newPodTemplate(configMapCreatedByUser.Name, ""), 1)
				require.NoError(cm.T(), err)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				require.Error(cm.T(), err)
				require.True(cm.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func (cm *ConfigmapsRBACTestSuite) TestUpdateConfigmap() {
	subSession := cm.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		cm.Run("Validate config map creation for user with role "+tt.role.String(), func() {
			adminProject, namespace, err := createProjectAndNamespace(cm.client, cm.cluster.ID)
			require.NoError(cm.T(), err)

			newUser, standardUserClient, err := rbac.CreateUserAndAddToClusterRole(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			require.NoError(cm.T(), err)
			cm.T().Logf("Created user: %v", newUser.Username)

			downstreamWranglerContext, err := cm.client.WranglerContext.DownStreamClusterWranglerContext(cm.cluster.ID)
			require.NoError(cm.T(), err)

			_, err = createConfigmap(namespace, downstreamWranglerContext)
			require.NoError(cm.T(), err)

			configMap.Data["foo1"] = "bar1"
			useDownstreamWranglerContext, err := standardUserClient.WranglerContext.DownStreamClusterWranglerContext(cm.cluster.ID)
			require.NoError(cm.T(), err)
			configMapCreatedByUser, err := useDownstreamWranglerContext.Core.ConfigMap().Update(configMap)

			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				require.NoError(cm.T(), err)
				_, err = dep.CreateDeployment(standardUserClient, cm.cluster.ID, deploymentName, namespace.Name, newPodTemplate(configMapCreatedByUser.Name, ""), 1)

			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				require.Error(cm.T(), err)
				require.True(cm.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func (cm *ConfigmapsRBACTestSuite) TestListConfigmaps() {
	subSession := cm.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}
	for _, tt := range tests {
		cm.Run("Validate config map creation for user with role "+tt.role.String(), func() {
			adminProject, namespace, err := createProjectAndNamespace(cm.client, cm.cluster.ID)
			require.NoError(cm.T(), err)

			newUser, standardUserClient, err := rbac.CreateUserAndAddToClusterRole(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			require.NoError(cm.T(), err)
			cm.T().Logf("Created user: %v", newUser.Username)

			adminWranglerContext, err := cm.client.WranglerContext.DownStreamClusterWranglerContext(cm.cluster.ID)
			require.NoError(cm.T(), err)
			configMapCreatedByAdmin, err := createConfigmap(namespace, adminWranglerContext)

			downstreamWranglerContext, err := standardUserClient.WranglerContext.DownStreamClusterWranglerContext(cm.cluster.ID)
			configMapListAsUser, err := downstreamWranglerContext.Core.ConfigMap().List(namespace.Name, metav1.ListOptions{
				FieldSelector: "metadata.name=" + configMapCreatedByAdmin.Name,
			})

			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				require.NoError(cm.T(), err)
				_, err = dep.CreateDeployment(standardUserClient, cm.cluster.ID, deploymentName, namespace.Name, newPodTemplate(configMapCreatedByAdmin.Name, ""), 1)
				require.NoError(cm.T(), err)
				require.Equal(cm.T(), len(configMapListAsUser.Items), 1)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				require.Error(cm.T(), err)
				require.True(cm.T(), k8sError.IsForbidden(err))
			}
		})
	}
}

func (cm *ConfigmapsRBACTestSuite) TestDeleteConfigmap() {
	subSession := cm.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		cm.Run("Validate config map creation for user with role "+tt.role.String(), func() {
			adminProject, namespace, err := createProjectAndNamespace(cm.client, cm.cluster.ID)
			require.NoError(cm.T(), err)

			newUser, standardUserClient, err := rbac.CreateUserAndAddToClusterRole(cm.client, tt.member, tt.role.String(), cm.cluster, adminProject)
			require.NoError(cm.T(), err)
			cm.T().Logf("Created user: %v", newUser.Username)

			adminDownstreamWranglerContext, err := cm.client.WranglerContext.DownStreamClusterWranglerContext(cm.cluster.ID)
			require.NoError(cm.T(), err)

			configMapCreatedByAdmin, err := createConfigmap(namespace, adminDownstreamWranglerContext)
			require.NoError(cm.T(), err)

			userDownstreamWranglerContext, err := standardUserClient.WranglerContext.DownStreamClusterWranglerContext(cm.cluster.ID)
			require.NoError(cm.T(), err)
			err = userDownstreamWranglerContext.Core.ConfigMap().Delete(namespace.Name, configMapCreatedByAdmin.Name, &metav1.DeleteOptions{})
			configMapListAsAdmin, errList := adminDownstreamWranglerContext.Core.ConfigMap().List(namespace.Name, metav1.ListOptions{
				FieldSelector: "metadata.name=" + configMapCreatedByAdmin.Name,
			})
			require.NoError(cm.T(), errList)

			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				require.NoError(cm.T(), err)
				require.Equal(cm.T(), len(configMapListAsAdmin.Items), 0)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				require.Error(cm.T(), err)
				require.True(cm.T(), k8sError.IsForbidden(err))
				require.Equal(cm.T(), len(configMapListAsAdmin.Items), 1)
			}
		})
	}
}

func TestConfigmapsRBACTestSuite(t *testing.T) {
	suite.Run(t, new(ConfigmapsRBACTestSuite))
}
