package main

import (
	"fmt"
	"log"
	"os"
	"time"

	//"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/floatingips"
	//"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/lbaas_v2/loadbalancers"
	//"github.com/gophercloud/gophercloud/pagination"
	"github.com/sapcc/kubernikus/pkg/api/client/operations"
	"github.com/sapcc/kubernikus/pkg/api/models"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/clientcmd"
)

func (s *E2ETestSuite) SetupSmokeTest() {
	// double check if cluster is ready for smoke test or exit
	s.isClusterUpOrWait()
	s.createClientset()
	s.getReadyPods()
	s.getReadyNodes()
	s.isClusterBigEnoughForSmokeTest()
	s.cleanUp()
	s.createPods()
	s.createServices()
}

func (s *E2ETestSuite) RunSmokeTest() {
	if s.IsTestNetwork || s.IsTestSmoke || s.IsTestAll {
		s.TestPod2PodCommunication()
	}

	if s.IsTestVolume || s.IsTestSmoke || s.IsTestAll {
		s.TestAttachVolume()
	}

}

func (s *E2ETestSuite) createClientset() {
	//TODO: this looks unnecessarily complicated
	s.getClusterKubeConfig()

	config, err := clientcmd.Load([]byte(s.KubeConfig))
	s.handleError(err)

	configOverrides := &clientcmd.ConfigOverrides{
		CurrentContext: config.CurrentContext,
	}

	kubeConfig := clientcmd.NewNonInteractiveClientConfig(
		*config,
		config.CurrentContext,
		configOverrides,
		nil,
	)

	clientConfig, err := kubeConfig.ClientConfig()
	s.handleError(err)

	clientSet, err := kubernetes.NewForConfig(clientConfig)
	s.handleError(err)

	s.clientSet = clientSet
}

func (s *E2ETestSuite) isClusterBigEnoughForSmokeTest() {
	nodeCount := len(s.readyNodes)
	if nodeCount < 2 {
		s.handleError(fmt.Errorf("found %v nodes in cluster. the smoke test requires a minimum of 2 nodes. aborting", nodeCount))
	}
}

func (s *E2ETestSuite) createPods() {
	for _, node := range s.readyNodes {
		//create pod
		pod, err := s.clientSet.CoreV1().Pods(Namespace).Create(&v1.Pod{
			ObjectMeta: meta_v1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s", NginxName, node.Name),
				Namespace: Namespace,
				Labels: map[string]string{
					"app":         NginxName,
					"nodeName":    node.Name,
					"hostNetwork": "true",
				},
			},
			Spec: v1.PodSpec{
				NodeName:    node.Name,
				HostNetwork: true,
				Containers: []v1.Container{
					{
						Image: NginxImage,
						Name:  NginxName,
						Ports: []v1.ContainerPort{
							{
								Name:          "http",
								ContainerPort: NginxPort,
							},
						},
					},
				},
			},
		})
		s.handleError(err)
		log.Printf("created pod %v/%v on node %v", pod.GetNamespace(), pod.GetName(), node.Name)

		// wait until ready
		w, err := s.clientSet.Pods(Namespace).Watch(meta_v1.SingleObject(
			meta_v1.ObjectMeta{
				Name: pod.GetName(),
			},
		))
		s.handleError(err)
		_, err = watch.Until(TimeoutPod, w, isPodRunning)
		s.handleError(err)
	}
	s.getReadyPods()
}

func (s *E2ETestSuite) createServices() {
	for _, node := range s.readyNodes {
		service := &v1.Service{
			ObjectMeta: meta_v1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s", NginxName, node.Name),
				Namespace: Namespace,
				Labels: map[string]string{
					"app":      NginxName,
					"podName":  NginxName,
					"nodeName": node.Name,
				},
			},
			Spec: v1.ServiceSpec{
				Ports: []v1.ServicePort{
					{
						Port:       NginxPort,
						TargetPort: intstr.FromInt(NginxPort),
					},
				},
				Type: v1.ServiceType(v1.ServiceTypeClusterIP),
				Selector: map[string]string{
					"app":      NginxName,
					"nodeName": node.Name,
				},
			},
		}

		log.Printf("created service %v/%v for pod one node %v", service.GetNamespace(), service.GetName(), node.Name)

		_, err := s.clientSet.Services(Namespace).Create(service)
		s.handleError(err)
	}
	s.getReadyServices()
}

func (s *E2ETestSuite) getReadyNodes() {
	nodes, err := s.clientSet.Nodes().List(meta_v1.ListOptions{
	//TODO: FieldSelector: fields.Set{"spec.unschedulable": "false"}.String(),
	})
	s.handleError(err)
	for _, node := range nodes.Items {
		log.Printf("found node: %s", node.Name)
	}
	s.readyNodes = nodes.Items
}

func (s *E2ETestSuite) getReadyPods() {
	pods, err := s.clientSet.Pods(Namespace).List(meta_v1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", NginxName),
	})
	s.handleError(err)
	for _, pod := range pods.Items {
		log.Printf("found pod %s/%s on node %s", pod.GetNamespace(), pod.GetName(), pod.Spec.NodeName)
	}
	s.readyPods = pods.Items
}

func (s *E2ETestSuite) getReadyServices() {
	services, err := s.clientSet.Services(Namespace).List(meta_v1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", NginxName),
	})
	s.handleError(err)
	for _, svc := range services.Items {
		log.Printf("found service %s/%s", svc.GetNamespace(), svc.GetName())
	}
	s.readyServices = services.Items
}

func (s *E2ETestSuite) TestAttachVolume() {
	nodeName := s.readyNodes[0].GetName()
	log.Print("testing persistent volume attachment on node %v", nodeName)

	s.createPVCForPod(nodeName)
	s.createPodWithMount(nodeName)
}

func (s *E2ETestSuite) createPVCForPod(nodeName string) {
	pvc, err := s.clientSet.PersistentVolumeClaims("default").Create(&v1.PersistentVolumeClaim{
		ObjectMeta: meta_v1.ObjectMeta{
			Namespace: Namespace,
			Name:      fmt.Sprintf("%v-%v", PVCName, nodeName),
			Labels: map[string]string{
				"app":      NginxName,
				"nodeName": nodeName,
			},
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse(PVCSize),
				},
			},
			Selector: &meta_v1.LabelSelector{
				MatchLabels: map[string]string{
					"nodeName": nodeName,
				},
			},
		},
	})
	s.handleError(err)
	log.Printf("created PVC %v/%v", pvc.GetNamespace(), pvc.GetName())

	_, err = s.waitForPVC(pvc)
	s.handleError(err)

	log.Printf("waiting for PVC %v/%v to be available", pvc.GetNamespace(), pvc.GetName())
}

func (s *E2ETestSuite) createPodWithMount(nodeName string) {
	pod, err := s.clientSet.Pods(Namespace).Create(&v1.Pod{
		ObjectMeta: meta_v1.ObjectMeta{
			Namespace: Namespace,
			Name:      PVCName,
			Labels: map[string]string{
				"app":      NginxName,
				"nodeName": nodeName,
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  PVCName,
					Image: NginxImage,
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      fmt.Sprintf("%v-%v", PVCName, nodeName),
							MountPath: "/tmp/mymount",
						},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: fmt.Sprintf("%v-%v", PVCName, nodeName),
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: PVCName,
						},
					},
				},
			},
		},
	})
	s.handleError(err)
	log.Printf("created pod %v/%v with mounted volume", pod.GetNamespace(), pod.GetName())

	log.Printf("waiting for pod %v/%v to become ready", pod.GetNamespace(), pod.GetName())
	// wait until ready
	w, err := s.clientSet.Pods(Namespace).Watch(meta_v1.SingleObject(
		meta_v1.ObjectMeta{
			Name: pod.GetName(),
		},
	))
	s.handleError(err)
	_, err = watch.Until(TimeoutPod, w, isPodRunning)
	s.handleError(err)

}

func (s *E2ETestSuite) TestPod2PodCommunication() {
	log.Print("testing network")

	log.Print("step 1: testing pod to pod")
	for _, source := range s.readyPods {
		for _, target := range s.readyPods {
			select {
			default:
				s.dialPodIP(&source, &target)
			case <-s.stopCh:
				os.Exit(1)
			}
		}
	}

	log.Printf("step 2: testing pod to service")
	for _, source := range s.readyPods {
		for _, target := range s.readyServices {
			select {
			default:
				s.dialServiceIP(&source, &target)
			case <-s.stopCh:
				os.Exit(1)
			}
		}
	}

	log.Print("network test done")
}

func (s *E2ETestSuite) dialPodIP(source *v1.Pod, target *v1.Pod) {
	_, err := s.dial(source, target.Status.PodIP, NginxPort)
	result := "success"
	if err != nil {
		result = "failure"
	}

	fmt.Printf("[%v] node/%30v --> node/%-30v   pod/%-15v --> pod/%-15v\n",
		result,
		source.Spec.NodeName,
		target.Spec.NodeName,
		source.Status.PodIP,
		target.Status.PodIP,
	)
}

func (s *E2ETestSuite) dialServiceIP(source *v1.Pod, target *v1.Service) {
	_, err := s.dial(source, target.Spec.ClusterIP, NginxPort)
	result := "success"
	if err != nil {
		result = "failure"
	}
	fmt.Printf("[%v] node/%30v --> node/%-30v   pod/%-15v --> svc/%-15v\n",
		result,
		source.Spec.NodeName,
		target.Labels["nodeName"],
		source.Status.PodIP,
		target.Spec.ClusterIP,
	)
}

func (s *E2ETestSuite) dial(sourcePod *v1.Pod, targetIP string, targetPort int32) (string, error) {
	cmd := fmt.Sprintf("wget --timeout=%v -O - http://%v:%v", TimeoutWGET, targetIP, targetPort)
	return RunHostCmd(sourcePod.GetNamespace(), sourcePod.GetName(), cmd)
}

func (s *E2ETestSuite) cleanUp() {
	log.Printf("cleaning up before running smoke tests")
	pods, err := s.clientSet.Pods(Namespace).List(meta_v1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", NginxName),
	})
	if err != nil {
		log.Fatalf("error while cleaning up smoke tests pods %v", err)
	}
	// pods
	for _, pod := range pods.Items {
		if err = s.clientSet.Pods(pod.GetNamespace()).Delete(pod.GetName(), &meta_v1.DeleteOptions{}); err != nil {
			log.Printf("could not delete pod %v/%v", pod.GetNamespace(), pod.GetName())
		}
		_, err = s.waitForPodDeleted(pod.GetNamespace(), pod.GetName())
		if err != nil {
			log.Print(err)
		}
	}
	// services
	svcs, err := s.clientSet.Services(Namespace).List(meta_v1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", NginxName),
	})
	if err != nil {
		log.Printf("error while cleaning smoke tests services %v", err)
	}
	for _, svc := range svcs.Items {
		if err = s.clientSet.Services(svc.GetNamespace()).Delete(svc.GetName(), &meta_v1.DeleteOptions{}); err != nil {
			log.Printf("could not delete service %v/%v", svc.GetNamespace(), svc.GetName())
		}
	}
	// pvcs
	pvcs, err := s.clientSet.PersistentVolumeClaims(Namespace).List(meta_v1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", NginxName),
	})
	if err != nil {
		log.Printf("error while cleaning smoke tests pvc %v", err)
	}
	for _, pvc := range pvcs.Items {
		if err = s.clientSet.PersistentVolumeClaims(pvc.GetNamespace()).Delete(pvc.GetName(), &meta_v1.DeleteOptions{}); err != nil {
			log.Printf("could not delete pvc %v/%v", pvc.GetNamespace(), pvc.GetName())
		}
	}
}

/*func (s *E2ETestSuite) TestCreateLoadBalancer() {

	pages := 0
	err := loadbalancers.List(s.neutronClient, nil).EachPage(func(page pagination.Page) (bool, error) {
		pages++
		lbs, err := loadbalancers.ExtractLoadBalancers(page)
		if err != nil {
			return false, err
		}
		for _, lb := range lbs {
			//TODO
			fmt.Println(lb.Name)
		}

		return true, nil
	})

	if err != nil {
		s.testing.Error(err)
	}

	// create fip
	fip, err := floatingips.Create(s.neutronClient,
		floatingips.CreateOpts{
		  Pool: "", //TODO
		}).Extract()

	if err != nil {
		s.testing.Error(err)
	}

	assert.Equal(s.testing, "READY", fip.ID)

	//TODO
	assiociateResult := floatingips2.AssociateInstance(
		s.neutronClient,
		"",
		floatingips2.AssociateOpts{
			FloatingIP: "",
		},
	)

	if err = assiociateResult.ExtractErr(); err != nil {
		s.testing.Error(err)
	}

	//TODO: curl or something
	getFIPResult, err := s.restClient.R().Get("fip")
	if err != nil {
		s.testing.Error(err)
	}

	//TODO: will this work?
	assert.Equal(s.testing, "200", getFIPResult.Status())

}*/

func (s *E2ETestSuite) isClusterUpOrWait() error {
	return s.waitForCluster(
		s.ClusterName,
		fmt.Sprintf("Cluster %s wasn't created in time", s.ClusterName),
		func(k *models.Cluster, err error) bool {
			if err != nil {
				log.Println(err)
				return false
			}
			if k.Status.Kluster.State == ClusterReady {
				log.Printf("Cluster %v is ready for smoke test", *k.Name)
				if isNodesHealthyAndRunning(k.Status.NodePools, k.Spec.NodePools) {
					return true
				}
			} else {
				log.Printf("Cluster %v not ready for smoke test. Still in %v", *k.Name, k.Status.Kluster.State)
			}
			return false
		},
	)
}

func (s *E2ETestSuite) waitForPVC(pvc *v1.PersistentVolumeClaim) (*v1.PersistentVolumeClaim, error) {
	w, err := s.clientSet.CoreV1().PersistentVolumeClaims(pvc.GetNamespace()).Watch(
		meta_v1.SingleObject(
			meta_v1.ObjectMeta{
				Name: pvc.GetName(),
			},
		),
	)
	if err != nil {
		return nil, err
	}

	_, err = watch.Until(5*time.Minute, w, isPVCBound)
	if err != nil {
		return nil, err
	}

	return pvc, nil
}

func isPVCBound(event watch.Event) (bool, error) {
	switch event.Type {
	case watch.Deleted:
		return false, errors.NewNotFound(schema.GroupResource{Resource: "pvc"}, "")
	}
	switch t := event.Object.(type) {
	case *v1.PersistentVolumeClaim:
		switch t.Status.Phase {
		case v1.ClaimBound:
			return true, nil
		case v1.ClaimPending, v1.ClaimLost:
			return false, fmt.Errorf("pvc still pending or already lost")
		}
	}
	return false, nil
}

func (s *E2ETestSuite) getClusterKubeConfig() {
	credentials, err := s.kubernikusClient.Operations.GetClusterCredentials(
		operations.NewGetClusterCredentialsParams().WithName(ClusterName),
		s.authFunc(),
	)
	s.handleError(err)
	cfg := credentials.Payload.Kubeconfig
	if cfg == "" {
		s.handleError(fmt.Errorf("kubeconfig of cluster %s is empty", ClusterName))
	}
	s.KubeConfig = cfg
}
