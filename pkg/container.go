/*
 * Copyright (c) 2020, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/NVIDIA/gpu-monitoring-tools/bindings/go/dcgm"
	"github.com/NVIDIA/gpu-monitoring-tools/bindings/go/nvml"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	v1lister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	// 8:memory:/kubepods/besteffort/pod187fd8f5-a423-4a1a-89b8-8928a71746a4/4923b671ebdf52ab54ad88084a2c7e0cf534504dde5dfbe7c84bdc4fe24cd46e
	// 6:memory:/kubepods/podc418bc7e-5ed8-11eb-801b-5254000a3b55/6ebf770b3e5d195576eca906e112c492507587cdfabc6d491caa2bd5d633cb6f
	kubePattern = regexp.MustCompile(`\d+:memory:/kubepods/(?:[^/]+/)?pod([^/]+)/([0-9a-f]{64})`)
)

type ContainerKey struct {
	PodUid      string
	ContainerId string
}

type ContainerInfo struct {
	PodUid      string
	ContainerId string

	PodName       string
	PodNamespace  string
	ContainerName string
}

func NewCgroupMapper(devices []dcgm.Device) *CgroupMapper {
	logrus.Infof("Container metrics collection enabled!")
	client, err := initKubeClient()
	if err != nil {
		logrus.Errorf("can't get kubernetes client: %s", err)
		return nil
	}
	podLister := newPodClients(client, v1.NamespaceAll)
	return &CgroupMapper{DeviceList: devices, PodLister: podLister}
}

func (c *CgroupMapper) Name() string {
	return "cgroupMapper"
}

func (c *CgroupMapper) K8sProcess() ([]ContainerMetric, error) {

	var metrics []ContainerMetric

	for _, device := range c.DeviceList {
		pids, err := listPidOnDev(device.UUID)
		if err != nil {
			return nil, err
		}

		containers, err := c.mapContainerPid(pids)
		if err != nil {
			return nil, err
		}

		utils, err := devGetAllProcessesUtilization(device.UUID)
		if err != nil {
			return nil, err
		}

		containerUtils, err := aggreContainersUtil(utils, containers)
		if err != nil {
			return nil, err
		}

		for container, util := range containerUtils {
			utilMap := make(map[string]string)
			utilMap["DCGM_FI_K8S_GPU_UTIL"] = fmt.Sprintf("%d", util.GPU)
			utilMap["DCGM_FI_K8S_MEM_COPY_UTIL"] = fmt.Sprintf("%d", util.Memory)
			utilMap["DCGM_FI_K8S_ENC_UTIL"] = fmt.Sprintf("%d", util.Encoder)
			utilMap["DCGM_FI_K8S_DEC_UTIL"] = fmt.Sprintf("%d", util.Decoder)

			for field, value := range utilMap {
				metrics = append(metrics, ContainerMetric{
					Name:  field,
					Value: value,

					GPU:       fmt.Sprintf("%d", device.GPU),
					GPUUUID:   device.UUID,
					GPUDevice: fmt.Sprintf("nvidia%d", device.GPU),

					Namespace: container.PodNamespace,
					Pod:       container.PodName,
					Container: container.ContainerName,
				})
			}
		}
	}
	logrus.Infof("metrics: %+v\n", metrics)
	return metrics, nil
}

func (c *CgroupMapper) Process(metrics [][]Metric) error {
	return nil
}

func (c *CgroupMapper) getPodInfo() (map[ContainerKey]ContainerInfo, error) {
	logrus.Infof("Get Pod and Container Information")

	pods, err := c.PodLister.List(labels.Everything())
	if err != nil {
		logrus.Infof("Get Pod and Container Information failed: %s", err)
		return nil, err
	}

	logrus.Infof("Get some Pod and Container(%d).", len(pods))
	containers := make(map[ContainerKey]ContainerInfo)
	for _, pod := range pods {
		fmt.Printf("pod name(%s), namespace(%s), uid(%s)\n", pod.ObjectMeta.Name, pod.ObjectMeta.Namespace, pod.ObjectMeta.UID)
		for _, container := range pod.Status.ContainerStatuses {
			containerId := strings.Replace(container.ContainerID, "docker://", "", 1)
			containers[ContainerKey{
				PodUid:      fmt.Sprintf("%s", pod.ObjectMeta.UID),
				ContainerId: containerId,
			}] = ContainerInfo{
				PodUid:        fmt.Sprintf("%s", pod.ObjectMeta.UID),
				ContainerId:   containerId,
				PodName:       pod.ObjectMeta.Name,
				PodNamespace:  pod.ObjectMeta.Namespace,
				ContainerName: container.Name,
			}
			fmt.Printf("container name(%s), id(%s)\n", container.Name, containerId)
		}
	}
	fmt.Printf("(getPodInfo) containers: %+v\n", containers)
	return containers, nil
}

func listPidOnDev(uuid string) ([]uint, error) {
	device, err := nvml.NewDeviceByUUID(uuid)
	if err != nil {
		return nil, fmt.Errorf("Error getting device %s: %v", uuid, err)
	}

	infos, err := device.GetAllRunningProcesses()
	if err != nil {
		return nil, fmt.Errorf("Error getting device %s processes: %v", uuid, err)
	}

	if len(infos) == 0 {
		return nil, nil
	}

	var pids []uint
	for _, info := range infos {
		pids = append(pids, info.PID)
	}
	fmt.Printf("[listPidOnDev] pids: %+v\n", pids)
	return pids, nil
}

func devGetAllProcessesUtilization(devUuid string) (map[uint]dcgm.UtilizationInfo, error) {
	util := make(map[uint]dcgm.UtilizationInfo)
	device, err := nvml.NewDeviceByUUID(devUuid)
	if err != nil {
		return nil, fmt.Errorf("Error getting device %s: %v", devUuid, err)
	}

	pInfo, err := device.GetAllProcessesUtilization()
	if err != nil {
		return nil, fmt.Errorf("Error getting device %s processes: %v", devUuid, err)
	}

	if len(pInfo) == 0 {
		return nil, nil
	}
	for _, info := range pInfo {
		util[info.PID] = dcgm.UtilizationInfo{
			GPU:     int64(info.SmUtil),
			Memory:  int64(info.MemUtil),
			Encoder: int64(info.EncUtil),
			Decoder: int64(info.DecUtil),
		}
	}

	return util, nil
}

func (c *CgroupMapper) mapContainerPid(pids []uint) (map[ContainerInfo][]uint, error) {
	infos, err := c.getPodInfo()
	if err != nil {
		logrus.Errorf("failed to get pod Info: %s", err)
		return nil, err
	}

	containers := make(map[ContainerInfo][]uint)
	for _, pid := range pids {
		fmt.Printf("[mapContainerPid] search pid(%d)\n", pid)
		f, err := os.Open(fmt.Sprintf("/rootfs/proc/%d/cgroup", pid))
		if err != nil {
			logrus.Errorf("open cgroup failed: %s", err)
			return nil, err
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			parts := kubePattern.FindStringSubmatch(line)
			fmt.Printf("[mapContainerPid] find pid(%d) parts: %+v\n", pid, parts)
			if parts != nil {
				value, ok := infos[ContainerKey{PodUid: parts[1], ContainerId: parts[2]}]
				if !ok {
					logrus.Errorf("container doesn't exist: %v", ContainerKey{PodUid: parts[1], ContainerId: parts[2]})
					return nil, fmt.Errorf("container doesn't exist: %v", ContainerKey{PodUid: parts[1], ContainerId: parts[2]})
				}
				containers[value] = append(containers[value], pid)
			} else {
				logrus.Errorf("[mapContainerPid] can't find container for pid(%d)", pid)
			}
		}
	}
	fmt.Printf("(mapContainerPid)containers: %+v\n", containers)
	return containers, nil
}

func aggreContainersUtil(utils map[uint]dcgm.UtilizationInfo, containers map[ContainerInfo][]uint) (map[ContainerInfo]dcgm.UtilizationInfo, error) {
	containerUtils := make(map[ContainerInfo]dcgm.UtilizationInfo)

	for container, pids := range containers {
		total := dcgm.UtilizationInfo{
			GPU:     0,
			Memory:  0,
			Encoder: 0,
			Decoder: 0,
		}
		for _, pid := range pids {
			util := utils[pid]
			total.GPU += util.GPU
			total.Memory += util.Memory
			total.Encoder += util.Encoder
			total.Decoder += util.Decoder
		}
		containerUtils[container] = total
	}

	return containerUtils, nil
}

func initKubeClient() (*kubernetes.Clientset, error) {
	logrus.Infof("Init Kube Client")
	kubeconfigFile := os.Getenv("KUBECONFIG")
	var err error
	var config *rest.Config

	if _, err = os.Stat(kubeconfigFile); err != nil {
		config, err = rest.InClusterConfig()
		if err != nil {
			logrus.Errorf("Failed due to %v", err)
			return nil, err
		}
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigFile)
		if err != nil {
			logrus.Errorf("Failed due to %v", err)
			return nil, err
		}
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		logrus.Errorf("Failed due to %v", err)
		return nil, err
	}

	logrus.Infof("Init KubeClient success.")
	return client, nil
}

// Creates clients watching pods: PodLister (listing only not terminated pods).
func newPodClients(kubeClient kubernetes.Interface, namespace string) v1lister.PodLister {
	// We are interested in pods which are Running or Unknown or Pending
	// We don't want succeeded and failed pods because they don't generate any usage anymore.
	selector := fields.ParseSelectorOrDie("status.phase!=" + string(v1.PodSucceeded) +
		",status.phase!=" + string(v1.PodFailed))
	podListWatch := cache.NewListWatchFromClient(kubeClient.CoreV1().RESTClient(), "pods", namespace, selector)
	indexer, controller := cache.NewIndexerInformer(
		podListWatch,
		&v1.Pod{},
		time.Hour,
		&cache.ResourceEventHandlerFuncs{},
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
	podLister := v1lister.NewPodLister(indexer)
	stopCh := make(chan struct{})
	go controller.Run(stopCh)
	if !cache.WaitForCacheSync(make(<-chan struct{}), controller.HasSynced) {
		logrus.Fatalf("Failed to sync pod cache during initialization")
	} else {
		logrus.Info("Initial pod synced successfully")
	}
	return podLister
}
