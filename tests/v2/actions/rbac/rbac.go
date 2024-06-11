package rbac

import (
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/rbac"
	"github.com/rancher/shepherd/extensions/users"
)

type Role string

const (
	RestrictedAdmin           Role = "restricted-admin"
	StandardUser              Role = "user"
	ClusterOwner              Role = "cluster-owner"
	ClusterMember             Role = "cluster-member"
	ProjectOwner              Role = "project-owner"
	ProjectMember             Role = "project-member"
	CreateNS                  Role = "create-ns"
	ReadOnly                  Role = "read-only"
	CustomManageProjectMember Role = "projectroletemplatebindings-manage"
	ActiveStatus                   = "active"
	ForbiddenError                 = "403 Forbidden"
	DefaultNamespace               = "fleet-default"
)

func (r Role) String() string {
	return string(r)
}

// CreateUserAndAddToClusterRole creates a user based on the global role and then adds the user to cluster with provided permissions.
func CreateUserAndAddToClusterRole(adminClient *rancher.Client, globalRole, clusterRole string, cluster *management.Cluster, project *management.Project) (*management.User, *rancher.Client, error) {
	user, userClient, err := SetupUser(adminClient, globalRole)
	if err != nil {
		return nil, nil, err
	}

	if globalRole == rbac.StandardUser.String() {
		if strings.Contains(clusterRole, "project") {
			err := users.AddProjectMember(adminClient, project, user, clusterRole, nil)
			if err != nil {
				return nil, nil, err
			}
		} else {
			err := users.AddClusterRoleToUser(adminClient, cluster, user, clusterRole, nil)
			if err != nil {
				return nil, nil, err
			}
		}
	}

	return user, userClient, nil
}

// SetupUser is a helper to create a global role and a client for the user.
func SetupUser(client *rancher.Client, globalRole string) (user *management.User, userClient *rancher.Client, err error) {
	user, err = users.CreateUserWithRole(client, users.UserConfig(), globalRole)
	if err != nil {
		return
	}
	userClient, err = client.AsUser(user)
	if err != nil {
		return
	}
	return
}
