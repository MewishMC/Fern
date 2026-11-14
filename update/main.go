package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const RepoURL = "https://github.com/MewishMC/Fern"
const ExeName = "Fern.exe"
const VersionURL = RepoURL + "/releases/latest"

var user32 = syscall.NewLazyDLL("user32.dll")
var MessageBox = user32.NewProc("MessageBoxW")

func MessageBoxW(title, message string) (int, error) {
	ret, _, err := MessageBox.Call(
		0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(message))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(title))),
		0x00000010,
	)
	if err != nil && err.Error() != "The operation completed successfully." {
		return 0, err
	}
	return int(ret), nil
}

func main() {
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "./"+ExeName)
		fmt.Println("No executable provided, using default: " + ExeName)
	}

	fmt.Println(os.Args[1])
	defer func() {
		if r := recover(); r != nil {
			stackBuf := make([]byte, 2048)
			stackSize := runtime.Stack(stackBuf, true)

			message := fmt.Sprintf("Panic Occured: %v\n\n%s", r, stackBuf[:stackSize])
			title := "Whoops, something went wrong!"

			if _, err := MessageBoxW(title, message); err != nil {
				panic(err)
			}
		}
	}()
	version, err := getLatestVersion()
	if err != nil {
		panic(err)
	}

	currentVersionHash, err := getCurrentHash()
	if err != nil {
		panic(err)
	}

	bytesV, err := getLatestVersionBytes(version)
	if err != nil {
		panic(err)
	}

	latestVersionHash := hashBytes(bytesV)
	procAttr := new(os.ProcAttr)
	procAttr.Files = []*os.File{os.Stdout, os.Stdout, os.Stdout}
	procAttr.Env = os.Environ()
	procAttr.Dir = filepath.Dir(os.Args[1])

	if latestVersionHash == currentVersionHash {
		if _, err := os.StartProcess(os.Args[1], append(os.Args[1:], "--launch"), procAttr); err != nil {
			panic(err)
		}
		os.Exit(0)
	}

	fmt.Println("Installing latest version...")
	if err := os.WriteFile(os.Args[1], bytesV, 0777); err != nil {
		panic(err)
	}

	_, err = os.StartProcess(os.Args[1], []string{"--launch"}, procAttr)
	if err != nil {
		panic(err)
	}
	os.Exit(0)
}

func getLatestVersion() (string, error) {
	req, _ := http.NewRequest("GET", VersionURL, nil)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("failed to get latest version - %s", res.Status)
	}
	breadcrumbs := strings.Split(res.Request.URL.String(), "/")
	return breadcrumbs[len(breadcrumbs)-1], nil
}

func hashBytes(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// getCurrentHash hashes the installed executable, or returns "none" when there
// is nothing installed yet so any release counts as newer.
func getCurrentHash() (string, error) {
	data, err := os.ReadFile(os.Args[1])
	if os.IsNotExist(err) {
		return "none", nil
	}
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func getLatestVersionBytes(version string) ([]byte, error) {
	url := fmt.Sprintf(RepoURL+"/releases/download/%s/"+ExeName, version)
	req, _ := http.NewRequest("GET", url, nil)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("failed to download latest version - %s", res.Status)
	}
	return io.ReadAll(res.Body)
}
