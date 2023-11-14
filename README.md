# Automatic Kernel Builds For Spitfire
`spitfire-build-kernel` is a small tool for building a Linux kernel image for use
in firecracker vms. It bundles deps (kernel tarball, configs, build tools etc) in a container image
so you have no depencies apart from docker (or podman, if that's how you swing)

The kernel image and kernel configs are all built using the same files used by
the firecracker team [here](https://github.com/firecracker-microvm/firecracker/tree/main/resources) (i just copied them lol!)

## Running
If you have go (`>= 1.19`) and docker installed, you can clone this repository and run
```
make
```
By default, it will build a container image, then create and run a container
(from the image) to build the kernel image.
**It stores the build artifact (i.e the kernel image) at `/tmp/kernel.image`**
Find the appropriate kernel image for your architecture in there.

## The CLI
```
Usage of spitfire-build-kernel:
  -arch string
        Architecture to compile kernel for, supported archs: x86_64 (default "x86_64")
  -build-image
        Build container that builds kernel
  -build-kernel
        Build the linux kernel
  -list-versions
        List kernel versions
  -run-container
        Run image tagged spitfire-build-kernel
  -version string
        Version of kernel to build (default "6.1")
```
