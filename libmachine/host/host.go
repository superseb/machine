package host

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/engine"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/mcndockerclient"
	"github.com/docker/machine/libmachine/mcnutils"
	"github.com/docker/machine/libmachine/provision"
	"github.com/docker/machine/libmachine/provision/pkgaction"
	"github.com/docker/machine/libmachine/provision/serviceaction"
	"github.com/docker/machine/libmachine/ssh"
	"github.com/docker/machine/libmachine/state"
	"github.com/docker/machine/libmachine/swarm"
)

var (
	validHostNameChars                = `^[a-zA-Z0-9][a-zA-Z0-9\-\.]*$`
	validHostNamePattern              = regexp.MustCompile(validHostNameChars)
	errMachineMustBeRunningForUpgrade = errors.New("Error: machine must be running to upgrade.")
)

type Host struct {
	ConfigVersion int
	Driver        drivers.Driver
	DriverName    string
	HostOptions   *Options
	Name          string
	RawDriver     []byte `json:"-"`
}

type Options struct {
	Driver        string
	Memory        int
	Disk          int
	EngineOptions *engine.Options
	SwarmOptions  *swarm.Options
	AuthOptions   *auth.Options
}

type Metadata struct {
	ConfigVersion int
	DriverName    string
	HostOptions   Options
}

func ValidateHostName(name string) bool {
	return validHostNamePattern.MatchString(name)
}

func (h *Host) RunSSHCommand(command string) (string, error) {
	return drivers.RunSSHCommandFromDriver(h.Driver, command)
}

func (h *Host) CreateSSHClient() (ssh.Client, error) {
	addr, err := h.Driver.GetSSHHostname()
	if err != nil {
		return ssh.ExternalClient{}, err
	}

	port, err := h.Driver.GetSSHPort()
	if err != nil {
		return ssh.ExternalClient{}, err
	}

	auth := &ssh.Auth{
		Keys: []string{h.Driver.GetSSHKeyPath()},
	}

	return ssh.NewClient(h.Driver.GetSSHUsername(), addr, port, auth)
}

func (h *Host) runActionForState(action func() error, desiredState state.State) error {
	if drivers.MachineInState(h.Driver, desiredState)() {
		return fmt.Errorf("Machine %q is already %s.", h.Name, strings.ToLower(desiredState.String()))
	}

	if err := action(); err != nil {
		return err
	}

	return mcnutils.WaitFor(drivers.MachineInState(h.Driver, desiredState))
}

func (h *Host) Start() error {
	return h.runActionForState(h.Driver.Start, state.Running)
}

func (h *Host) Stop() error {
	return h.runActionForState(h.Driver.Stop, state.Stopped)
}

func (h *Host) Kill() error {
	return h.runActionForState(h.Driver.Kill, state.Stopped)
}

func (h *Host) Restart() error {
	if drivers.MachineInState(h.Driver, state.Running)() {
		if err := h.Stop(); err != nil {
			return err
		}

		if err := mcnutils.WaitFor(drivers.MachineInState(h.Driver, state.Stopped)); err != nil {
			return err
		}
	}

	if err := h.Start(); err != nil {
		return err
	}

	if err := mcnutils.WaitFor(drivers.MachineInState(h.Driver, state.Running)); err != nil {
		return err
	}

	return nil
}

func (h *Host) Upgrade() error {
	machineState, err := h.Driver.GetState()
	if err != nil {
		return err
	}

	if machineState != state.Running {
		return errMachineMustBeRunningForUpgrade
	}

	provisioner, err := provision.DetectProvisioner(h.Driver)
	if err != nil {
		return err
	}

	log.Info("Upgrading docker...")
	if err := provisioner.Package("docker", pkgaction.Upgrade); err != nil {
		return err
	}

	log.Info("Restarting docker...")
	return provisioner.Service("docker", serviceaction.Restart)
}

func (h *Host) URL() (string, error) {
	return h.Driver.GetURL()
}

func (h *Host) AuthOptions() *auth.Options {
	return h.HostOptions.AuthOptions
}

func (h *Host) DockerVersion() (string, error) {
	return mcndockerclient.DockerVersion(h)
}

func (h *Host) ConfigureAuth() error {
	provisioner, err := provision.DetectProvisioner(h.Driver)
	if err != nil {
		return err
	}

	// TODO: This is kind of a hack (or is it?  I'm not really sure until
	// we have more clearly defined outlook on what the responsibilities
	// and modularity of the provisioners should be).
	//
	// Call provision to re-provision the certs properly.
	if err := provisioner.Provision(swarm.Options{}, *h.HostOptions.AuthOptions, *h.HostOptions.EngineOptions); err != nil {
		return err
	}

	return nil
}
