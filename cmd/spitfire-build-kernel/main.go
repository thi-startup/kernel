package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/template"
	"time"
)

var (
	arch         = flag.String("arch", "x86_64", "Architecture to compile kernel for, supported archs: x86_64")
	buildImage   = flag.Bool("build-image", false, "Build container that builds kernel")
	runContainer = flag.Bool("run-container", false, "Run image tagged spitfire-build-kernel")
	buildKernel  = flag.Bool("build-kernel", false, "Build the linux kernel")

	// see https://www.kernel.org/releases.json
	latest = "https://cdn.kernel.org/pub/linux/kernel/v5.x/linux-5.10.188.tar.xz"

	// got this from the `resources` subdirectory in github.com/firecracker-microvm/firecracker
	config = "https://raw.githubusercontent.com/firecracker-microvm/firecracker/main/resources/guest_configs/microvm-kernel-x86_64-5.10.config"
)

// this process in the docker container is passed the argument `-build-kernel`
// so it doesn't try to run a docker container but goes straight to downloading
// and building the kernel
var dockerfile = `
FROM debian:bullseye
RUN apt-get update && apt-get install -y {{ .BuildPackage }} bc libssl-dev wget bison flex kmod
COPY spitfire-build-kernel /usr/bin/spitfire-build-kernel
RUN echo 'spitfire:x:{{ .Uid }}:{{ .Gid }}:nobody:/:/bin/sh' >> /etc/passwd && \
    chown -R {{ .Uid }}:{{ .Gid }} /usr/src
USER spitfire
WORKDIR /usr/src
ENTRYPOINT ["/usr/bin/spitfire-build-kernel", "-arch", "{{ .Arch }}", "-build-kernel"]
`

var dockerfileTmpl = template.Must(template.New("dockerfile").
	Funcs(map[string]any{
		"basename": func(path string) string {
			return filepath.Base(path)
		},
	}).Parse(dockerfile),
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

	defconfig := command("make", fmt.Sprintf("ARCH=%s", *arch), "olddefconfig")
	if err := defconfig.Run(); err != nil {
		return err
	}

	// we don't want debug info
	if err := command("scripts/config", "--disable", "DEBUG_INFO").Run(); err != nil {
		return err
	}
	if err := command("scripts/config", "--enable", "DEBUG_INFO_NONE").Run(); err != nil {
		return err
	}

	// TODO: we probably should be stripping kernel too, we don't need the debug symbols

	// change if we are building for arm64
	binary := "vmlinux"

	build := command("make", fmt.Sprintf("-j%d", runtime.NumCPU()), binary)
	build.Env = append(os.Environ(),
		"KBUILD_TIME="+time.Now().Format("Mon Jan 2 15:04:05 MST 2006"),
	)
	if err := build.Run(); err != nil {
		return fmt.Errorf("make kernel: %w", err)
	}

	if !exists(binary) {
		return fmt.Errorf("built the kernel alright, but cannot find the kernel image")
	}

	imagePath := "/tmp/kernel.image"
	if !exists(imagePath) {
		if err := os.Mkdir(imagePath, 0o776); err != nil {
			return err
		}
	}
	if err := copyFile(binary, filepath.Join(imagePath, binary)); err != nil {
		return err
	}

	return nil
}

func getContainerExecutable() (string, error) {
	choices := []string{"docker", "podman"}
	for _, exe := range choices {
		p, err := exec.LookPath(exe)
		if err != nil {
			continue
		}
		res, err := filepath.EvalSymlinks(p)
		if err != nil {
			return "", err
		}
		return res, err
	}
	return "", fmt.Errorf("none of %v in $PATH", choices)
}

func dockerImage(workdir string) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	log.Printf("creating dockerfile -> %s/Dockerfile", workdir)
	dockerFile, err := os.Create(filepath.Join(workdir, "Dockerfile"))
	if err != nil {
		return err
	}

	// TODO: changes with the architecture
	archSpecificPacakages := "build-essential"

	if err := dockerfileTmpl.Execute(dockerFile, struct {
		Uid          string
		Gid          string
		Arch         string
		BuildPackage string
	}{
		Uid:          u.Uid,
		Gid:          u.Gid,
		Arch:         *arch,
		BuildPackage: archSpecificPacakages,
	}); err != nil {
		return err
	}

	if err := dockerFile.Close(); err != nil {
		return err
	}

	log.Printf("building container for kernel compilation")
	execName, err := getContainerExecutable()
	if err != nil {
		return err
	}
	dockerBuild := command(execName,
		"build",
		"--rm=true",
		"--tag=spitfire-build-kernel",
		".",
	)
	dockerBuild.Dir = workdir
	if err := dockerBuild.Run(); err != nil {
		return err
	}
	return nil
}

func main() {
	flag.Parse()

	workdir, err := os.MkdirTemp(os.TempDir(), "spitfire-build-dir")
	if err != nil {
		log.Fatal(fmt.Errorf("failed to create temp workdir: %w", err))
	}
	defer os.RemoveAll(workdir)

	if *buildImage {
		builder, err := os.Executable()
		if err != nil {
			log.Fatal(err)
		}
		if err := copyFile(builder, filepath.Join(workdir, "spitfire-build-kernel")); err != nil {
			log.Fatal(fmt.Errorf("failed to copy spitfire-build-kernel executable to %s", workdir))
		}

		if err := dockerImage(workdir); err != nil {
			log.Fatal(fmt.Errorf("error creating Dockerfile: %w", err))
		}
	}

	// runs on the host
	if *runContainer {
		execName, err := getContainerExecutable()
		if err != nil {
			log.Fatal(err)
		}

		buildresult := "/tmp/kernel.image"
		if !exists(buildresult) {
			if err := os.MkdirAll(buildresult, 0o776); err != nil {
				log.Fatalf("failed to create build result dir: %v", err)
			}
		}
		exe := filepath.Base(execName)

		log.Printf("running container: working directory: %s, build results: %s", workdir, buildresult)

		var dockerRun *exec.Cmd
		if exe == "docker" {
			dockerRun = command(execName,
				"run",
				"--rm",
				"--volume", buildresult+":/tmp/kernel.image:Z",
				"spitfire-build-kernel",
			)
		} else {
			dockerRun = command(execName,
				"run",
				"--rm",
				"--userns=keep-id",
				"--volume", buildresult+":/tmp/kernel.image:Z",
				"spitfire-build-kernel",
			)
		}
		dockerRun.Dir = workdir
		if err := dockerRun.Run(); err != nil {
			log.Fatalf("%s run: %v (cmd: %v)", execName, err, dockerRun.Args)
		}
		return
	}

	// this part runs in the container tagged `spitfire-build-kernel`
	if *buildKernel {
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
}
