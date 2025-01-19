package modules

import (
	"fmt"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"os/exec"
	"strings"
)

//Gonna have toggles for what the person wants to dispaly
// If you find a better way to get the cpu usage redo it

func getGPUUsage() {
	nvidiaCmd := exec.Command("nvidia-smi", "--query-gpu=utilization.gpu", "--format=csv,noheader,nounits")
	nvidiaOut, err := nvidiaCmd.Output()
	if err == nil {
		fmt.Printf("NVIDIA GPU Usage: %s%%\n", strings.TrimSpace(string(nvidiaOut)))
	}

	amdCmd := exec.Command("rocm-smi", "--showpower")
	amdOut, err := amdCmd.Output()
	if err == nil {
		fmt.Printf("AMD GPU Usage: %s\n", strings.TrimSpace(string(amdOut)))
	}

	intelCmd := exec.Command("intel_gpu_top", "-J")
	intelOut, err := intelCmd.Output()
	if err == nil {
		fmt.Printf("Intel GPU Usage: %s\n", strings.TrimSpace(string(intelOut)))
	}
}

func GetCpuInfo(cpuName bool, CpuCores bool) string {
	cpuInfo, err := cpu.Info()
	if err != nil {
		return err.Error()
	}

	CPUname := strings.Join(strings.Fields(cpuInfo[0].ModelName)[:3], " ")
	CPUsomethings := []struct {
		flag bool
		text string
	}{
		{cpuName, fmt.Sprintf("ᶜᵖᵘ %s", CPUname)},
		{CpuCores, fmt.Sprintf("| ᶜᵒʳᵉˢ %d", len(cpuInfo))},
	}

	var result []string
	for _, condition := range CPUsomethings {
		if !condition.flag {
			continue
		}

		result = append(result, condition.text)
	}

	return strings.Join(result, " ")
}

func GetMem() string {
	virtualMemory, _ := mem.VirtualMemory()
	total := float64(virtualMemory.Total) / (1024 * 1024 * 1024)
	used := float64(virtualMemory.Used) / (1024 * 1024 * 1024)
	return fmt.Sprintf("RAM: %.2f / %.2f GB ", used, total)
}
