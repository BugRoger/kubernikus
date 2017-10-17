package helm

import (
	"github.com/sapcc/kubernikus/pkg/apis/kubernikus/v1"
	yaml "gopkg.in/yaml.v2"
)

func KlusterToHelmValues(kluster *v1.Kluster, certificates map[string]string, bootstrapToken string) ([]byte, error) {
	type Values struct {
		Openstack struct {
			AuthURL    string `yaml:"authURL"`
			Username   string `yaml:"username"`
			Password   string `yaml:"password"`
			DomainName string `yaml:"domainName"`
			ProjectID  string `yaml:"projectID"`
			Region     string `yaml:"Region"`
			LbSubnetID string `yaml:"lbSubnetID"`
			RouterID   string `yaml:"routerID"`
		}
		Certs            map[string]string `yaml:"certs,omitempty"`
		ClusterCIDR      string            `yaml:"clusterCIDR,omitempty"`
		ServiceCIDR      string            `yaml:"serviceCIDR,omitempty"`
		AdvertiseAddress string            `yaml:"advertiseAddress,omitempty"`
		BoostrapToken    string            `yaml:"bootstrapToken,omitempty"`
	}

	values := Values{}
	values.BoostrapToken = bootstrapToken
	values.Certs = certificates
	values.Openstack.Password = kluster.Secret.Openstack.Password

	values.Openstack.AuthURL = kluster.Spec.Openstack.AuthURL
	values.Openstack.Username = kluster.Spec.Openstack.Username
	values.Openstack.DomainName = kluster.Spec.Openstack.Domain
	values.Openstack.ProjectID = kluster.Spec.Openstack.ProjectID
	values.Openstack.Region = kluster.Spec.Openstack.Region
	values.Openstack.LbSubnetID = kluster.Spec.Openstack.LBSubnetID
	values.Openstack.RouterID = kluster.Spec.Openstack.RouterID
	values.ClusterCIDR = kluster.Spec.ClusterCIDR
	values.ServiceCIDR = kluster.Spec.ServiceCIDR
	values.AdvertiseAddress = kluster.Spec.AdvertiseAddress

	result, err := yaml.Marshal(values)
	if err != nil {
		return nil, err
	}

	return result, nil
}
