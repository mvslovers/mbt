// Package mvsctl starts and stops a local MVS/CE in docker: `mbt mvs up` and
// `mbt mvs down`, the run-mvs / stop-mvs targets mvsMF and rexx370 carried
// as near-identical Makefile copies (design §9).
//
// up: create the network if missing; start the container if it exists, else
// create it from the image with the ports published; inside a container
// itself (/.dockerenv), join that network too. down: stop the container.
// Configured by MBT_DOCKER_NETWORK (mvs-net), MBT_MVS_CONTAINER (mvs),
// MBT_MVS_IMAGE (ghcr.io/mvslovers/mvsce-builder) and MBT_MVS_PORTS
// ("1080 3270 8888").
package mvsctl

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Config names the network, container, image and ports.
type Config struct {
	Network, Container, Image string
	Ports                     []string
}

// FromEnv reads the configuration, with the defaults of the Makefiles.
func FromEnv(get func(string) string) Config {
	def := func(k, d string) string {
		if v := strings.TrimSpace(get(k)); v != "" {
			return v
		}
		return d
	}
	return Config{
		Network:   def("MBT_DOCKER_NETWORK", "mvs-net"),
		Container: def("MBT_MVS_CONTAINER", "mvs"),
		Image:     def("MBT_MVS_IMAGE", "ghcr.io/mvslovers/mvsce-builder"),
		Ports:     strings.Fields(def("MBT_MVS_PORTS", "1080 3270 8888")),
	}
}

// Docker runs docker with args; it returns stdout and whether it succeeded.
type Docker func(args ...string) (string, error)

// Available says whether the docker CLI is on PATH.
func Available() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// RealDocker runs the docker CLI.
func RealDocker(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		// a missing network or container is an answer, not noise
		return strings.TrimSpace(out.String()), fmt.Errorf("%v: %s", err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Up starts the MVS container, creating network and container as needed.
func Up(c Config, docker Docker, inContainer bool, hostname string, log func(string)) error {
	if _, err := docker("network", "inspect", c.Network); err != nil {
		if _, err := docker("network", "create", c.Network); err != nil {
			return fmt.Errorf("cannot create docker network %s: %v", c.Network, err)
		}
	}
	if _, err := docker("inspect", c.Container); err == nil {
		running, _ := docker("inspect", "-f", "{{.State.Running}}", c.Container)
		if running == "true" {
			log(fmt.Sprintf("%s is already running", c.Container))
		} else {
			log(fmt.Sprintf("Starting %s...", c.Container))
			if _, err := docker("start", c.Container); err != nil {
				return fmt.Errorf("cannot start %s: %v", c.Container, err)
			}
		}
	} else {
		log(fmt.Sprintf("Creating %s from %s (this may take a while)...", c.Container, c.Image))
		args := []string{"run", "-d", "--name", c.Container, "--network", c.Network}
		for _, p := range c.Ports {
			args = append(args, "-p", p+":"+p)
		}
		args = append(args, c.Image)
		if _, err := docker(args...); err != nil {
			return fmt.Errorf("cannot create %s: %v", c.Container, err)
		}
	}
	if inContainer {
		docker("network", "connect", c.Network, hostname) // already connected is fine
	}
	return nil
}

// Down stops the container.
func Down(c Config, docker Docker, log func(string)) error {
	if _, err := docker("inspect", c.Container); err != nil {
		log(fmt.Sprintf("%s does not exist", c.Container))
		return nil
	}
	log(fmt.Sprintf("Stopping %s...", c.Container))
	if _, err := docker("stop", c.Container); err != nil {
		return fmt.Errorf("cannot stop %s: %v", c.Container, err)
	}
	return nil
}
