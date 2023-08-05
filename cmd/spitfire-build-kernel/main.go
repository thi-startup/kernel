package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var (
	arch = "x86_64"

	// see https://www.kernel.org/releases.json
	latest = "https://cdn.kernel.org/pub/linux/kernel/v5.x/linux-5.10.188.tar.xz"

	// see
	config = "https://raw.githubusercontent.com/firecracker-microvm/firecracker/main/resources/guest_configs/microvm-kernel-x86_64-5.10.config"
)

// if an `outfile` is not provided, downloaded artifact is stored
// as `filepath.Base(url)`
func download(url string, outfile string) error {
	if outfile == "" {
		outfile = filepath.Base(url)
	}

	out, err := os.Create(outfile)
	if err != nil {
		return err
	}
	defer out.Close()
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		return fmt.Errorf("unexpected HTTP status code for %s: got %d, want %d", url, got, want)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	return nil
}

func exists(name string) bool {
	if _, err := os.Stat(name); os.IsNotExist(err) {
		return false
	}
	return true
}

func copyFile(src, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	fi, err := srcFile.Stat()
	if err != nil {
		return err
	}

	destFile, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE, fi.Mode())
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		return err
	}

	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get stats for %s", src)
	}

	if err := destFile.Chown(int(stat.Uid), int(stat.Gid)); err != nil {
		return fmt.Errorf("chown failed: %v", err)
	}

	return nil
}

// exec.Command but sets the stdout and stderr to calling process's stdout and stderr
func command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func compile() error {
	if exists(".config") {
		if err := command("make", "distclean").Run(); err != nil {
			return err
		}
		if err := command("make", "clean").Run(); err != nil {
			return err
		}
	}

	if err := copyFile("kernel.config", ".config"); err != nil {
		return err
	}

	defconfig := command("make", fmt.Sprintf("ARCH=%s", arch), "olddefconfig")
	if err := defconfig.Run(); err != nil {
		return err
	}

	binary := "vmlinux"

	build := command("make", fmt.Sprintf("-j%d", runtime.NumCPU()), binary)
	build.Env = append(os.Environ(),
		"KBUILD_TIME="+time.Now().Format("Mon Jan 2 15:04:05 MST 2006"),
	)
	if err := build.Run(); err != nil {
		return fmt.Errorf("make kernel: %w", err)
	}
	return nil
}

func main() {
	kernel := filepath.Base(latest)
	ksrc := strings.TrimSuffix(kernel, ".tar.xz")
	kernelConfig := filepath.Base(config)

	if !exists(kernel) {
		log.Printf("downloading the kernel source -> %s", kernel)
		if err := download(latest, kernel); err != nil {
			log.Fatal(fmt.Errorf("failed to download kernel: %w", err))
		}

		log.Printf("downloading kernel config -> %s", kernelConfig)
		if err := download(config, kernelConfig); err != nil {
			log.Fatal(fmt.Errorf("failed to download kernel config: %w", err))
		}
	}

	if !exists(ksrc) {
		log.Printf("untar kernel -> %s", ksrc)
		untar := command("tar", "xf", kernel)
		if err := untar.Run(); err != nil {
			log.Fatal(fmt.Errorf("failed to untar: %w", err))
		}
	}

	configDest := filepath.Join(ksrc, "kernel.config")
	log.Printf("copying kernel config -> %s", configDest)
	if err := copyFile(filepath.Base(config), configDest); err != nil {
		log.Fatal(fmt.Errorf("failed to copy kernel.config: %w", err))
	}

	log.Printf("compiling the kernel")
	if err := os.Chdir(ksrc); err != nil {
		log.Fatal(err)
	}
	if err := compile(); err != nil {
		log.Fatal(fmt.Errorf("kernel compile failed: %w", err))
	}
}
