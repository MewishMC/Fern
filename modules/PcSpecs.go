package modules

import (
	"fmt"
	"github.com/shirou/gopsutil/mem"
)

func GetMem() string {
	virtualMemory, _ := mem.VirtualMemory()
	total := float64(virtualMemory.Total) / (1024 * 1024 * 1024)
	used := float64(virtualMemory.Used) / (1024 * 1024 * 1024)
	return fmt.Sprintf("RAM: %.2f / %.2f GB ", used, total)
}
