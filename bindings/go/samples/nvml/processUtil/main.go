package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/NVIDIA/gpu-monitoring-tools/bindings/go/nvml"
)

const (
	PINFOHEADER = `# gpu   pid   type  mem  Command
# Idx     #   C/G   MiB  name`
)

func main() {
	getPodInfo()
}

func getUtilInfo() {
	nvml.Init()
	defer nvml.Shutdown()

	count, err := nvml.GetDeviceCount()
	if err != nil {
		log.Panicln("Error getting device count:", err)
	}

	var devices []*nvml.Device
	for i := uint(0); i < count; i++ {
		device, err := nvml.NewDevice(i)
		if err != nil {
			log.Panicf("Error getting device %d: %v\n", i, err)
		}
		devices = append(devices, device)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(time.Second * 1)
	defer ticker.Stop()

	fmt.Println(PINFOHEADER)
	for {
		select {
		case <-ticker.C:
			for i, device := range devices {
				pInfo, err := device.GetAllProcessesUtilization()
				if err != nil {
					log.Panicf("Error getting device %d processes: %v\n", i, err)
				}
				if len(pInfo) == 0 {
					fmt.Printf("%5v %5s %5s %5s %-5s\n", i, "-", "-", "-", "-")
				}
				for j := range pInfo {
					fmt.Printf("%5v %5v %5v %5v %5v %5v\n",
						i, pInfo[j].PID, pInfo[j].SmUtil, pInfo[j].DecUtil, pInfo[j].EncUtil, pInfo[j].MemUtil)
				}
			}
		case <-sigs:
			return
		}
	}
}

func getPodInfo() {
	nvml.Init()
	defer nvml.Shutdown()

	count, err := nvml.GetDeviceCount()
	if err != nil {
		log.Panicln("Error getting device count:", err)
	}

	var devices []*nvml.Device
	for i := uint(0); i < count; i++ {
		device, err := nvml.NewDevice(i)
		if err != nil {
			log.Panicf("Error getting device %d: %v\n", i, err)
		}
		devices = append(devices, device)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(time.Second * 1)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			for i, device := range devices {
				pInfo, err := device.GetAllProcessesUtilization()
				if err != nil {
					log.Panicf("Error getting device %d processes: %v\n", i, err)
				}
				if len(pInfo) == 0 {
					fmt.Printf("%5v %5s %5s %5s %-5s\n", i, "-", "-", "-", "-")
				}
				for j := range pInfo {
					fmt.Printf("%5v %5v %5v %5v %5v %5v\n",
						i, pInfo[j].PID, pInfo[j].SmUtil, pInfo[j].DecUtil, pInfo[j].EncUtil, pInfo[j].MemUtil)
					parser(pInfo[j].PID)
				}
			}
		case <-sigs:
			return
		}
	}
}

/*
12:freezer:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
11:rdma:/
10:blkio:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
9:perf_event:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
8:cpu,cpuacct:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
7:devices:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
6:cpuset:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
5:pids:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
4:net_cls,net_prio:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
3:memory:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
2:hugetlb:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
1:name=systemd:/kubepods/besteffort/pod8a75b771-cf2e-45c2-b01d-3a008a8c90c7/597f450d1f61f07cc3c2df60ea98b4769eb22437ce352a55cfb30f3dc514161b
0::/system.slice/dockerd.service
*/

var (
	kubePattern = regexp.MustCompile(`\d+:memory:/kubepods/[^/]+/pod([^/]+)/([0-9a-f]{64})`)
	// dockerPattern = regexp.MustCompile(`\d+:.+:/docker/pod[^/]+/([0-9a-f]{64})`)
)

func parser(pid uint) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		// this is normal, it just means the PID no longer exists
		log.Fatal(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// parts := dockerPattern.FindStringSubmatch(line)
		// if parts != nil {
		// 	fmt.Println(parts[1])
		// }
		parts := kubePattern.FindStringSubmatch(line)
		if parts != nil {
			fmt.Println(parts)
		}
	}
}
