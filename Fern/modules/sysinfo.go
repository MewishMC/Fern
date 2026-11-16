package modules

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// System lines: how long the PC and Fern have been up, disk usage and the
// laptop battery. Everything here is a plain Windows call that needs no
// rights or extra software.

var (
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")
	procGetSystemPowerStatus = kernel32.NewProc("GetSystemPowerStatus")
)

// PCUptime is the time since Windows booted. Fast startup keeps the clock
// running across a shutdown, as Task Manager's uptime does.
func PCUptime() time.Duration {
	if procGetTickCount64.Find() != nil {
		return 0
	}
	ms, _, _ := procGetTickCount64.Call()
	return time.Duration(ms) * time.Millisecond
}

// FernUptime is the time since Fern started.
func FernUptime() time.Duration { return time.Since(fernStartedAt) }

var (
	unitsShort = []string{"d", "h", "m", "s"}
	unitsLong  = []string{"day", "hour", "minute", "second"}
)

// formatUptime renders d in a style, showing at most units units for the
// short and long styles, starting at the largest non-zero one.
func formatUptime(d time.Duration, style string, units int) string {
	if d < 0 {
		d = 0
	}
	secs := int64(d / time.Second)
	if style == UptimeClock {
		return fmt.Sprintf("%d:%02d", secs/3600, secs/60%60)
	}
	vals := []int64{secs / 86400, secs / 3600 % 24, secs / 60 % 60, secs % 60}
	start := 0
	for start < 3 && vals[start] == 0 {
		start++
	}
	if units < 1 {
		units = 1
	}
	var parts []string
	for i := start; i < len(vals) && len(parts) < units; i++ {
		// Seconds only when nothing bigger is shown: "3m" not "3m 12s"
		// unless asked for three or more units.
		if i == 3 && start < 3 && units < 3 {
			break
		}
		if style == UptimeLong {
			name := unitsLong[i]
			if vals[i] != 1 {
				name += "s"
			}
			parts = append(parts, strconv.FormatInt(vals[i], 10)+" "+name)
		} else {
			parts = append(parts, strconv.FormatInt(vals[i], 10)+unitsShort[i])
		}
	}
	return strings.Join(parts, " ")
}

func uptimeText(o UptimeOptions, pc, fern time.Duration) string {
	var parts []string
	if o.ShowPC {
		parts = append(parts, strings.TrimSpace(o.PCText+" "+formatUptime(pc, o.Style, o.Units)))
	}
	if o.ShowFern {
		parts = append(parts, strings.TrimSpace(o.FernText+" "+formatUptime(fern, o.Style, o.Units)))
	}
	return withLabel(o.Label, strings.Join(parts, " · "))
}

func uptimeLine(cfg Config) string {
	return uptimeText(cfg.UptimeOptions, PCUptime(), FernUptime())
}

// ---------------------------------------------------------------- disks

// DiskInfo is one drive.
type DiskInfo struct {
	Drive string `json:"drive"` // "C:"
	Label string `json:"label"` // volume name
	Total uint64 `json:"total"` // bytes
	Free  uint64 `json:"free"`
	Fixed bool   `json:"fixed"` // an internal drive, not a USB stick or card
}

var (
	diskMu      sync.Mutex
	diskList    []DiskInfo
	diskSampled time.Time
	diskBusy    bool
)

// listDisks reads every local drive with media in it. Network drives are
// left out, since a sleeping share can stall the call for seconds.
func listDisks() []DiskInfo {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	// No "insert a disk" dialog for an empty card reader.
	old := windows.SetErrorMode(windows.SEM_FAILCRITICALERRORS | windows.SEM_NOOPENFILEERRORBOX)
	defer windows.SetErrorMode(old)
	var out []DiskInfo
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		drive := string(rune('A'+i)) + ":"
		root, _ := syscall.UTF16PtrFromString(drive + `\`)
		t := windows.GetDriveType(root)
		if t != windows.DRIVE_FIXED && t != windows.DRIVE_REMOVABLE {
			continue
		}
		var avail, total, free uint64
		if windows.GetDiskFreeSpaceEx(root, &avail, &total, &free) != nil || total == 0 {
			continue
		}
		d := DiskInfo{Drive: drive, Total: total, Free: free, Fixed: t == windows.DRIVE_FIXED}
		var name [windows.MAX_PATH + 1]uint16
		if windows.GetVolumeInformation(root, &name[0], uint32(len(name)), nil, nil, nil, nil, 0) == nil {
			d.Label = syscall.UTF16ToString(name[:])
		}
		out = append(out, d)
	}
	return out
}

// Disks returns the drives as last read. A read older than ten seconds starts
// a new one in the background, since a USB disk spinning up can take a while
// to answer and the chatbox must not wait for it.
func Disks() []DiskInfo {
	diskMu.Lock()
	defer diskMu.Unlock()
	if !diskBusy && (diskSampled.IsZero() || time.Since(diskSampled) > 10*time.Second) {
		diskBusy = true
		go func() {
			list := listDisks()
			diskMu.Lock()
			diskList, diskSampled, diskBusy = list, time.Now(), false
			diskMu.Unlock()
		}()
	}
	return slices.Clone(diskList)
}

// systemDrive is the drive Windows is on.
func systemDrive() string {
	if d := cleanDrive(strings.TrimSpace(windowsEnv("SystemDrive"))); d != "" {
		return d
	}
	return "C:"
}

func windowsEnv(key string) string {
	v, _ := syscall.Getenv(key)
	return v
}

// diskSize prints bytes as GB, or TB from 1000 GB, counting in 1024s like
// Explorer does.
func diskSize(b uint64) string {
	gb := float64(b) / (1 << 30)
	if gb >= 1000 {
		return trimNum(gb/1024, 1) + " TB"
	}
	if gb < 10 {
		return trimNum(gb, 1) + " GB"
	}
	return strconv.Itoa(int(math.Round(gb))) + " GB"
}

// sizePair prints "412/931 GB", sharing the unit when both use the same one.
func sizePair(used, total uint64) string {
	u, t := diskSize(used), diskSize(total)
	us, ts := strings.Fields(u), strings.Fields(t)
	if len(us) == 2 && len(ts) == 2 && us[1] == ts[1] {
		return us[0] + "/" + t
	}
	return u + "/" + t
}

func diskEntry(d DiskInfo, o DiskOptions) string {
	used := d.Total - d.Free
	var parts []string
	switch {
	case o.ShowUsed && o.ShowTotal:
		parts = append(parts, sizePair(used, d.Total))
	case o.ShowUsed:
		parts = append(parts, diskSize(used))
	case o.ShowTotal:
		parts = append(parts, diskSize(d.Total))
	}
	if o.ShowFree {
		parts = append(parts, diskSize(d.Free)+" free")
	}
	if o.ShowPercent && d.Total > 0 {
		parts = append(parts, strconv.Itoa(int(math.Round(float64(used)*100/float64(d.Total))))+"%")
	}
	if len(parts) == 0 {
		return ""
	}
	text := strings.Join(parts, " · ")
	if o.ShowLetter {
		text = d.Drive + " " + text
	}
	return text
}

func diskText(disks []DiskInfo, o DiskOptions, sysDrive string) string {
	want := o.Drives
	if len(want) == 0 {
		want = []string{sysDrive}
	}
	var parts []string
	for _, w := range want {
		for _, d := range disks {
			if strings.EqualFold(d.Drive, w) {
				if e := diskEntry(d, o); e != "" {
					parts = append(parts, e)
				}
				break
			}
		}
	}
	return withLabel(o.Label, strings.Join(parts, o.Separator))
}

func diskLine(cfg Config) string {
	return diskText(Disks(), cfg.DiskOptions, systemDrive())
}

// ---------------------------------------------------------------- battery

// PowerStatus is the PC's own battery, from GetSystemPowerStatus.
type PowerStatus struct {
	HasBattery  bool `json:"hasBattery"`
	OnAC        bool `json:"onAC"`
	Charging    bool `json:"charging"`
	Percent     int  `json:"percent"`     // -1 when unknown
	SecondsLeft int  `json:"secondsLeft"` // -1 when unknown
	Saver       bool `json:"saver"`       // battery saver is on
}

type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

// powerFrom decodes SYSTEM_POWER_STATUS.
func powerFrom(s systemPowerStatus) PowerStatus {
	p := PowerStatus{
		HasBattery:  s.BatteryFlag != 128 && s.BatteryFlag != 255,
		OnAC:        s.ACLineStatus == 1,
		Charging:    s.BatteryFlag != 255 && s.BatteryFlag&8 != 0,
		Percent:     -1,
		SecondsLeft: -1,
		Saver:       s.SystemStatusFlag&1 != 0,
	}
	if s.BatteryLifePercent <= 100 {
		p.Percent = int(s.BatteryLifePercent)
	}
	if s.BatteryLifeTime != 0xFFFFFFFF {
		p.SecondsLeft = int(s.BatteryLifeTime)
	}
	return p
}

// readPower is swapped in tests.
var readPower = func() PowerStatus {
	if procGetSystemPowerStatus.Find() != nil {
		return PowerStatus{Percent: -1, SecondsLeft: -1}
	}
	var s systemPowerStatus
	if r, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s))); r == 0 {
		return PowerStatus{Percent: -1, SecondsLeft: -1}
	}
	return powerFrom(s)
}

// CurrentPower reports the PC's battery.
func CurrentPower() PowerStatus { return readPower() }

func pcBatteryText(p PowerStatus, o PCBatteryOptions) string {
	if !p.HasBattery || p.Percent < 0 || (o.HideOnAC && p.OnAC) {
		return ""
	}
	icon := o.Icon
	if p.Percent <= o.LowThreshold && o.LowIcon != "" && !p.Charging {
		icon = o.LowIcon
	}
	text := strings.TrimSpace(icon + " " + strconv.Itoa(p.Percent) + "%")
	switch {
	case p.Charging:
		text += o.ChargingMark
	case p.OnAC:
		text += o.PluggedMark
	}
	if o.ShowTime && !p.OnAC && p.SecondsLeft > 0 {
		text += " · " + formatUptime(time.Duration(p.SecondsLeft)*time.Second, UptimeShort, 2) + " left"
	}
	return withLabel(o.Label, text)
}

func pcBatteryLine(cfg Config) string {
	return pcBatteryText(CurrentPower(), cfg.PCBatteryOptions)
}
