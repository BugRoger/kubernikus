package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/golang/glog"
	"github.com/sapcc/kubernikus/pkg/api/client/operations"
	"github.com/sapcc/kubernikus/pkg/api/models"

	"k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func (s *E2ETestSuite) waitForCluster(klusterName, errorString string, waitUntilFunc func(k *models.Cluster, err error) bool) error {
	for start := time.Now(); time.Since(start) < Timeout; time.Sleep(CheckInterval) {
		select {
		default:
			kluster, err := s.kubernikusClient.Operations.ShowCluster(
				operations.NewShowClusterParams().WithName(klusterName),
				s.authFunc(),
			)
			if kluster != nil {
				if waitUntilFunc(kluster.Payload, err) {
					return nil
				}
			} else {
				if waitUntilFunc(nil, err) {
					return nil
				}
			}
		case <-s.stopCh:
			os.Exit(1)
		}
	}
	return fmt.Errorf(errorString)
}

func isNodePoolsUpdated(nodePools []*models.ClusterSpecNodePoolsItems0) bool {
	// check if both nodePools exists and one of the is medium nodePool
	for _, v := range nodePools {
		if *v.Name == "medium" && len(nodePools) == 2 {
			return true
		}
	}
	return false
}

func isNodesHealthyAndRunning(nodePoolsStatus []*models.ClusterStatusNodePoolsItems0, nodePoolsSpec []*models.ClusterSpecNodePoolsItems0) bool {
	for _, v := range nodePoolsStatus {
		expectedSize := getNodePoolSizeFromSpec(nodePoolsSpec, *v.Name)
		if expectedSize == -1 {
			log.Printf("couldn't find nodepool with name %v in spec", *v.Name)
			return false
		}
		if *v.Healthy != expectedSize || *v.Running != expectedSize {
			log.Printf("nodepool %v: expected %v node, actual: healthy %v, running %v", *v.Name, expectedSize, *v.Healthy, *v.Running)
			return false
		}
	}
	return true
}

func getNodePoolSizeFromSpec(nodePoolsSpec []*models.ClusterSpecNodePoolsItems0, name string) int64 {
	for _, v := range nodePoolsSpec {
		if *v.Name == name {
			return *v.Size
		}
	}
	return -1
}

func (s *E2ETestSuite) emptyNodePoolsOfKluster() error {

	log.Printf("stopping all nodes of cluster %v", s.ClusterName)

	cluster, err := s.kubernikusClient.Operations.ShowCluster(
		operations.NewShowClusterParams().WithName(s.ClusterName),
		s.authFunc(),
	)
	if err != nil {
		return err
	}

	nodePools := cluster.Payload.Spec.NodePools

	for _, nodePool := range nodePools {
		*nodePool.Size = 0
	}

	// empty node pools
	_, err = s.kubernikusClient.Operations.UpdateCluster(
		operations.NewUpdateClusterParams().
			WithName(s.ClusterName).
			WithBody(&models.Cluster{
				Name: &s.ClusterName,
				Spec: models.ClusterSpec{
					NodePools: nodePools,
				},
			},
			),
		s.authFunc(),
	)
	if err != nil {
		return err
	}

	s.waitForCluster(
		s.ClusterName,
		fmt.Sprintf("NodePool of cluster %v still has nodes in state running", s.ClusterName),
		func(k *models.Cluster, err error) bool {
			if err != nil {
				log.Println(err)
				return false
			}
			for _, node := range k.Status.NodePools {
				if *node.Running != 0 {
					log.Printf("NodePools of cluster %v has nodes in state running", *k.Name)
					return false
				}
			}
			return true
		},
	)

	return nil
}

func newE2ECluster(klusterName *string) *models.Cluster {
	nodeImage := "coreos-stable-amd64"
	nodeNameSmall := "small"
	nodeFlavorSmall := "m1.small"
	nodePoolSizeSmall := int64(ClusterSmallNodePoolSize)

	return &models.Cluster{
		Name: klusterName,
		Spec: models.ClusterSpec{
			NodePools: []*models.ClusterSpecNodePoolsItems0{
				{
					Name:   &nodeNameSmall,
					Flavor: &nodeFlavorSmall,
					Image:  nodeImage,
					Size:   &nodePoolSizeSmall,
				},
			},
		},
	}
}

func mediumNodePoolItem() *models.ClusterSpecNodePoolsItems0 {
	nodePoolName := "medium"
	nodePoolImage := "coreos-stable-amd64"
	nodePoolFlavor := "m1.medium"
	nodePoolSize := int64(ClusterMediumNodePoolSize)

	return &models.ClusterSpecNodePoolsItems0{
		Name:   &nodePoolName,
		Image:  nodePoolImage,
		Flavor: &nodePoolFlavor,
		Size:   &nodePoolSize,
	}
}

func isPodRunning(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Deleted:
		return false, errors.NewNotFound(schema.GroupResource{Resource: "pods"}, "")
	}
	switch t := event.Object.(type) {
	case *v1.Pod:
		switch t.Status.Phase {
		case v1.PodRunning:
			return true, nil
		case v1.PodFailed, v1.PodSucceeded:
			return false, fmt.Errorf("pod failed or ran to completion")
		}
	}
	return false, nil
}

func (s *E2ETestSuite) waitForPodDeleted(namespace, name string) (bool, error) {
	for start := time.Now(); time.Since(start) < TimeoutPod; time.Sleep(CheckInterval) {
		select {
		default:
			_, err := s.clientSet.Pods(namespace).Get(name, meta_v1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					log.Printf("pod %v/%v was deleted", namespace, name)
					return true, nil
				}
				return false, err
			}
			log.Printf("pod %v/%v still exists", namespace, name)
		case <-s.stopCh:
			os.Exit(1)
		}
	}
	return false, nil
}

func (s *E2ETestSuite) handleError(err error, isTearDownCluster ...bool) {
	if err == nil {
		return
	}
	glog.Error(err)
	// cleanup
	if isTearDownCluster != nil && isTearDownCluster[0] {
		s.tearDownCluster()
	}
	os.Exit(1)
}

func (s *E2ETestSuite) tearDownCluster() {
	s.emptyNodePoolsOfKluster()
	log.Printf("Deleting cluster %v", s.ClusterName)

	_, err := s.kubernikusClient.Operations.TerminateCluster(
		operations.NewTerminateClusterParams().WithName(s.ClusterName),
		s.authFunc(),
	)
	s.handleError(fmt.Errorf("unable to delete cluster %v: %v", s.ClusterName, err))

	if err := s.waitForCluster(
		s.ClusterName,
		fmt.Sprintf("Cluster %s wasn't terminated in time", s.ClusterName),
		func(k *models.Cluster, err error) bool {
			if err != nil {
				switch err.(type) {
				case *operations.ShowClusterDefault:
					result := err.(*operations.ShowClusterDefault)
					if result.Code() == 404 {
						log.Printf("Cluster %v was terminated", s.ClusterName)
						return true
					}
				case error:
					log.Println("Failed to show cluster %v: %v", s.ClusterName, err)
					return false
				}
			}
			log.Printf("Cluster %v was not terminated. Still in %v", s.ClusterName, k.Status.Kluster.State)
			return false
		},
	); err != nil {
		s.handleError(fmt.Errorf("Error while waiting for cluster %v to be terminated: %v", s.ClusterName, err))
	}
}

func (s *E2ETestSuite) NewClientSet() {
	config := s.newClientConfig()
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Couldn't create Kubernetes client: %s", err)
		return
	}
	s.clientSet = clientSet
}

func (s *E2ETestSuite) newClientConfig() *rest.Config {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}

	if s.KubeConfig != "" {
		rules.ExplicitPath = s.KubeConfig
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		fmt.Errorf("Couldn't get Kubernetes default config: %s", err)
	}

	return config
}
