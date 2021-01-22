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

	"github.com/NVIDIA/gpu-monitoring-tools/bindings/go/dcgm"
	"github.com/NVIDIA/gpu-monitoring-tools/bindings/go/nvml"
	"github.com/sirupsen/logrus"
)

var (
	kubePattern = regexp.MustCompile(`\d+:memory:/kubepods/[^/]+/pod([^/]+)/([0-9a-f]{64})`)
)

type PodContainer struct {
	PodUid      string
	ContainerId string
}

func NewCgroupMapper(devices []dcgm.Device) *CgroupMapper {
	logrus.Infof("Kubernetes metrics (by cgroup) collection enabled!")
	return &CgroupMapper{DeviceList: devices}
}

func (c *CgroupMapper) Name() string {
	return "cgroupMapper"
}

func (c *CgroupMapper) K8sProcess() ([]ContainerMetric, error) {

	var metrics []ContainerMetric

	for _, device := range c.DeviceList {
		fmt.Println("GPU: ", device.GPU)
		pids, err := listPidOnDev(device.UUID)
		if err != nil {
			return nil, err
		}

		utils, err := getUtilInfo(device.UUID)
		if err != nil {
			return nil, err
		}

		for _, pid := range pids {
			pod, err := getPodContainer(pid)
			if err == nil {
				if util, ok := utils[pid]; ok {
					utilMap := make(map[string]string)
					utilMap["DCGM_FI_K8S_GPU_UTIL"] = string(util.GPU)
					utilMap["DCGM_FI_K8S_MEM_COPY_UTIL"] = string(util.Memory)
					utilMap["DCGM_FI_K8S_ENC_UTIL"] = string(util.Encoder)
					utilMap["DCGM_FI_K8S_DEC_UTIL"] = string(util.Decoder)

					for field, value := range utilMap {
						metrics = append(metrics, ContainerMetric{
							Name:  field,
							Value: value,

							GPU:       fmt.Sprintf("%d", device.GPU),
							GPUUUID:   device.UUID,
							GPUDevice: fmt.Sprintf("nvidia%d", device.GPU),

							Namespace: "default",
							Pod:       pod.PodUid,
							Container: pod.ContainerId,
						})
					}
					fmt.Printf("util(%d): %+v\n", pid, util)
					fmt.Printf("pod: %+v\n", pod)
				}
			}
		}

	}
	return metrics, nil
}

func (c *CgroupMapper) Process(metrics [][]Metric) error {
	return nil
}

func getPodContainer(pid uint) (PodContainer, error) {
	f, err := os.Open(fmt.Sprintf("/rootfs/proc/%d/cgroup", pid))
	if err != nil {
		logrus.Errorf("open cgroup failed: %s", err)
		return PodContainer{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := kubePattern.FindStringSubmatch(line)
		if parts != nil {
			return PodContainer{PodUid: parts[1], ContainerId: parts[2]}, nil
		}
	}
	return PodContainer{}, fmt.Errorf("could't find pod by pid(%d)", pid)
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
	return pids, nil
}

func getUtilInfo(uuid string) (map[uint]dcgm.UtilizationInfo, error) {
	util := make(map[uint]dcgm.UtilizationInfo)
	device, err := nvml.NewDeviceByUUID(uuid)
	if err != nil {
		return nil, fmt.Errorf("Error getting device %s: %v", uuid, err)
	}

	pInfo, err := device.GetAllProcessesUtilization()
	if err != nil {
		return nil, fmt.Errorf("Error getting device %s processes: %v", uuid, err)
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
