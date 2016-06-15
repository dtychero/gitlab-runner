package machine

import (
	"errors"
	"fmt"
	"github.com/stretchr/testify/assert"
	"gitlab.com/gitlab-org/gitlab-ci-multi-runner/common"
	"gitlab.com/gitlab-org/gitlab-ci-multi-runner/helpers/docker"
	"strings"
	"testing"
	"time"
)

var machineDefaultConfig = &common.RunnerConfig{
	RunnerSettings: common.RunnerSettings{
		Machine: &common.DockerMachine{
			MachineName: "%s",
			IdleTime:    5,
		},
	},
}

var machineCreateFail = &common.RunnerConfig{
	RunnerSettings: common.RunnerSettings{
		Machine: &common.DockerMachine{
			MachineName: "create-fail-%s",
			IdleTime:    5,
		},
	},
}

var machineProvisionFail = &common.RunnerConfig{
	RunnerSettings: common.RunnerSettings{
		Machine: &common.DockerMachine{
			MachineName: "provision-fail-%s",
			IdleTime:    5,
		},
	},
}

var machineSecondFail = &common.RunnerConfig{
	RunnerSettings: common.RunnerSettings{
		Machine: &common.DockerMachine{
			MachineName: "second-fail-%s",
			IdleTime:    5,
		},
	},
}

var machineNoConnect = &common.RunnerConfig{
	RunnerSettings: common.RunnerSettings{
		Machine: &common.DockerMachine{
			MachineName: "no-connect-%s",
			IdleTime:    5,
		},
	},
}

func createMachineConfig(idleCount int, idleTime int) *common.RunnerConfig {
	return &common.RunnerConfig{
		RunnerSettings: common.RunnerSettings{
			Machine: &common.DockerMachine{
				MachineName: "test-machine-%s",
				IdleCount:   idleCount,
				IdleTime:    idleTime,
			},
		},
	}
}

type testMachine struct {
	machines []string
	second   bool
}

func (m *testMachine) Create(driver, name string, opts ...string) error {
	if strings.Contains(name, "second-fail") {
		if !m.second {
			m.second = true
			return errors.New("Failed to create")
		}
	} else if strings.Contains(name, "create-fail") || strings.Contains(name, "provision-fail") {
		return errors.New("Failed to create")
	}
	m.machines = append(m.machines, name)
	return nil
}

func (m *testMachine) Provision(name string) error {
	if strings.Contains(name, "provision-fail") || strings.Contains(name, "second-fail") {
		return errors.New("Failed to provision")
	}
	m.machines = append(m.machines, name)
	return nil
}

func (m *testMachine) Remove(name string) error {
	var machines []string
	for _, machine := range m.machines {
		if machine != name {
			machines = append(machines, machine)
		}
	}
	m.machines = machines
	return nil
}

func (m *testMachine) Exist(name string) bool {
	if strings.Contains(name, "no-can-connect") {
		return false
	}
	for _, machine := range m.machines {
		if machine == name {
			return true
		}
	}
	return false
}

func (m *testMachine) List(nodeFilter string) (machines []string, err error) {
	return m.machines, nil
}

func (m *testMachine) CanConnect(name string) bool {
	if strings.Contains(name, "no-can-connect") {
		return false
	}
	return true
}

func (m *testMachine) Credentials(name string) (dc docker_helpers.DockerCredentials, err error) {
	if strings.Contains(name, "no-connect") {
		err = errors.New("Failed to connect")
	}
	return
}

func countIdleMachines(p *machineProvider) (count int) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	for _, details := range p.details {
		if details.State == machineStateIdle {
			count++
		}
	}
	return
}

func assertIdleMachines(t *testing.T, p *machineProvider, expected int, msgAndArgs ...interface{}) bool {
	var idle int
	for i := 0; i < 10; i++ {
		idle = countIdleMachines(p)

		if expected == idle {
			return true
		}

		time.Sleep(50 * time.Microsecond)
	}

	result := fmt.Sprintf("should have %d idle, but has %d", expected, idle)
	assert.Fail(t, result, msgAndArgs...)
	return false
}

func countTotalMachines(p *machineProvider) (count int) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	for _, details := range p.details {
		if details.State != machineStateRemoving {
			count++
		}
	}
	return
}

func assertTotalMachines(t *testing.T, p *machineProvider, expected int, msgAndArgs ...interface{}) bool {
	var total int
	for i := 0; i < 10; i++ {
		total = countTotalMachines(p)

		if expected == total {
			return true
		}

		time.Sleep(50 * time.Microsecond)
	}

	result := fmt.Sprintf("should have %d total, but has %d", expected, total)
	assert.Fail(t, result, msgAndArgs...)
	return false
}

func testMachineProvider(machines ...string) (*machineProvider, *testMachine) {
	t := &testMachine{
		machines: machines,
	}
	p := &machineProvider{
		details: make(machinesDetails),
		machine: t,
	}
	return p, t
}

func TestMachineDetails(t *testing.T) {
	p, _ := testMachineProvider()
	m1 := p.machineDetails("test", false)
	assert.NotNil(t, m1, "returns a new machine")
	assert.Equal(t, machineStateIdle, m1.State)

	m2 := p.machineDetails("test", false)
	assert.Equal(t, m1, m2, "returns the same machine")

	m3 := p.machineDetails("test", true)
	assert.Equal(t, machineStateAcquired, m3.State, "acquires machine")

	m4 := p.machineDetails("test", true)
	assert.Nil(t, m4, "fails to return re-acquired machine")

	m5 := p.machineDetails("test", false)
	assert.Equal(t, m1, m5, "returns acquired machine")
	assert.Equal(t, machineStateAcquired, m5.State, "machine is acquired")
}

func TestMachineFindFree(t *testing.T) {
	p, _ := testMachineProvider("no-can-connect", "machine1", "machine2")
	d1 := p.findFreeMachine()
	assert.Nil(t, d1, "no machines, return nil")

	d2 := p.findFreeMachine("machine1")
	assert.NotNil(t, d2, "acquire one machine")

	d3 := p.findFreeMachine("machine1")
	assert.Nil(t, d3, "fail to acquire that machine")

	d4 := p.findFreeMachine("machine1", "machine2")
	assert.NotNil(t, d4, "acquire a new machine")
	assert.NotEqual(t, d2, d4, "and it's a different machine")

	d5 := p.findFreeMachine("machine1", "no-can-connect")
	assert.Nil(t, d5, "fails to acquire machine to which he can't connect")
}

func TestMachineCreationAndRemoval(t *testing.T) {
	provisionRetryInterval = 0

	p, _ := testMachineProvider()
	d, errCh := p.create(machineDefaultConfig, machineStateUsed)
	assert.NotNil(t, d)
	assert.NoError(t, <-errCh)
	assert.Equal(t, machineStateUsed, d.State)
	assert.NotNil(t, p.details[d.Name])

	d2, errCh := p.create(machineProvisionFail, machineStateUsed)
	assert.NotNil(t, d2)
	assert.Error(t, <-errCh, "Fails, because it fails to provision machine")
	assert.Equal(t, machineStateRemoving, d2.State)

	d3, errCh := p.create(machineCreateFail, machineStateUsed)
	assert.NotNil(t, d3)
	assert.NoError(t, <-errCh)
	assert.Equal(t, machineStateUsed, d3.State)

	p.remove(d.Name)
	assert.Equal(t, machineStateRemoving, d.State)
}

func TestMachineUse(t *testing.T) {
	provisionRetryInterval = 0

	p, _ := testMachineProvider("machine1")

	_, d1, err := p.findAndUseMachine(machineDefaultConfig)
	assert.NotNil(t, d1)
	assert.NoError(t, err)
	assert.Equal(t, machineStateAcquired, d1.State)
	assert.Equal(t, "machine1", d1.Name, "finds a free machine1")

	_, d2, err := p.findAndUseMachine(machineProvisionFail)
	assert.Nil(t, d2, "fails to find a machine")
	assert.NoError(t, err, "this is not an error")
}

func TestMachineTestRetry(t *testing.T) {
	provisionRetryInterval = 0
	useMachineRetryInterval = 0
	useMachineRetries = 3

	p, _ := testMachineProvider()
	_, d, err := p.retryFindAndUseMachine(machineSecondFail)
	assert.Nil(t, d, "fails to find a free machine")
	assert.NoError(t, err, "this is not an error")
}

func TestUseCredentials(t *testing.T) {
	p, _ := testMachineProvider()

	details := p.machineDetails("machine1", true)
	_, err := p.useCredentials(nil, details)
	assert.NoError(t, err, "successfully gets credentials")

	_, err = p.useCredentials(nil, details)
	assert.NoError(t, err, "successfully gets credentials for second time")

	details = p.machineDetails("no-connect", true)
	_, err = p.useCredentials(nil, details)
	assert.Error(t, err, "fails to get credentials when can connect to machine")
	assert.Equal(t, machineStateIdle, details.State, "machine should be released")

	details = p.machineDetails("no-can-connect", true)
	_, err = p.useCredentials(nil, details)
	assert.NoError(t, err, "successfully gets credentials for the first time")
	assert.Equal(t, machineStateAcquired, details.State, "machine should be acquired")

	_, err = p.useCredentials(nil, details)
	assert.Error(t, err, "fails to get credentials when cannnot connect")
	assert.Equal(t, machineStateIdle, details.State, "machine should be released")
}

func TestCreateAndUseMachine(t *testing.T) {
	p, _ := testMachineProvider()

	_, d, err := p.createAndUseMachine(machineDefaultConfig)
	assert.NoError(t, err, "succesfully creates machine")
	assert.Equal(t, machineStateAcquired, d.State, "machine should be acquired")

	_, d, err = p.createAndUseMachine(machineNoConnect)
	assert.Error(t, err, "fails to create a machine")
}

func TestMachineAcquireAndRelease(t *testing.T) {
	p, _ := testMachineProvider("test-machine")

	d1, err := p.Acquire(machineDefaultConfig)
	assert.NoError(t, err)
	assert.NotNil(t, d1, "acquires machine")

	d2, _ := p.Acquire(machineDefaultConfig)
	assert.Nil(t, d2, "fails to acquire a machine")

	p.Release(machineDefaultConfig, d1)

	d3, err := p.Acquire(machineDefaultConfig)
	assert.NoError(t, err)
	assert.Equal(t, d1, d3, "acquires released machine")
}

func TestMachineOnDemandMode(t *testing.T) {
	p, _ := testMachineProvider()

	config := createMachineConfig(0, 1)
	_, err := p.Acquire(config)
	assert.NoError(t, err)
}

func TestMachinePreCreateMode(t *testing.T) {
	p, _ := testMachineProvider()

	config := createMachineConfig(1, 5)
	d, err := p.Acquire(config)
	assert.Error(t, err, "it should fail with message that currently there's no free machines")
	assert.Nil(t, d)
	assertIdleMachines(t, p, 1, "it should contain exactly one machine")

	d, err = p.Acquire(config)
	assert.NoError(t, err, "it should be ready to process builds")
	assertIdleMachines(t, p, 0, "it should acquire the free node")
	p.Release(config, d)
	assertIdleMachines(t, p, 1, "after releasing it should have one free node")

	config = createMachineConfig(2, 5)
	d, err = p.Acquire(config)
	assert.NoError(t, err)
	p.Release(config, d)
	assertIdleMachines(t, p, 2, "it should start creating a second machine")

	config = createMachineConfig(1, 0)
	config.Limit = 1
	d, err = p.Acquire(config)
	assert.NoError(t, err)
	p.Release(config, d)
	assertIdleMachines(t, p, 1, "it should downscale to single machine")

	d, err = p.Acquire(config)
	assert.NoError(t, err, "we should acquire single machine")

	_, err = p.Acquire(config)
	assert.Error(t, err, "it should fail with message that currently there's no free machines")
	p.Release(config, d)
	assertIdleMachines(t, p, 1, "it should leave one idle")
}

func TestMachineLimitMax(t *testing.T) {
	p, _ := testMachineProvider()

	config := createMachineConfig(10, 5)
	config.Limit = 5

	d, err := p.Acquire(config)
	assert.Error(t, err, "it should fail with message that currently there's no free machines")
	assert.Nil(t, d)
	assertIdleMachines(t, p, 5, "it should contain exactly a maximum of 5 nodes")

	config.Limit = 8
	d, err = p.Acquire(config)
	p.Release(config, d)
	assertIdleMachines(t, p, 8, "it should upscale to 8 nodes")

	config.Limit = 2
	d, err = p.Acquire(config)
	p.Release(config, d)
	assertIdleMachines(t, p, 2, "it should downscale to 2 nodes")
}

func TestMachineMaxBuilds(t *testing.T) {
	provisionRetryInterval = 0

	p, _ := testMachineProvider("machine1")

	config := createMachineConfig(1, 5)
	config.Machine.MaxBuilds = 1
	d, err := p.Acquire(config)
	assert.NoError(t, err)
	assert.NotNil(t, d)

	_, nd, err := p.Use(config, d)
	assert.NoError(t, err)
	assert.Equal(t, d, nd, "we passed the data, we should get the same data now")

	p.Release(config, d)

	dd := d.(*machineDetails)
	assert.Equal(t, machineStateIdle, dd.State, "the machine should still be in idle")

	_, err = p.Acquire(config)
	assert.Equal(t, machineStateRemoving, dd.State, "provider should get removed due to too many builds")
	assert.Equal(t, "Too many builds", dd.Reason, "provider should get removed due to too many builds")
}

func TestMachineIdleLimits(t *testing.T) {
	p, _ := testMachineProvider()

	config := createMachineConfig(2, 1)
	d, errCh := p.create(config, machineStateIdle)
	assert.NoError(t, <-errCh, "machine creation should not fail")

	d2, err := p.Acquire(config)
	p.Release(config, d2)
	assert.NoError(t, err)
	assert.Equal(t, machineStateIdle, d.State, "machine should not be removed, because is still in idle time")

	config = createMachineConfig(2, 0)
	d3, err := p.Acquire(config)
	p.Release(config, d3)
	assert.NoError(t, err)
	assert.Equal(t, machineStateIdle, d.State, "machine should not be removed, because no more than two idle")

	config = createMachineConfig(0, 0)
	d4, err := p.Acquire(config)
	p.Release(config, d4)
	assert.NoError(t, err)
	assert.Equal(t, machineStateRemoving, d.State, "machine should not be removed, because no more than two idle")
	assert.Equal(t, "Too many idle machines", d.Reason)
}

func TestMachineUseOnDemand(t *testing.T) {
	provisionRetryInterval = 0

	p, _ := testMachineProvider()

	_, nd, err := p.Use(machineDefaultConfig, nil)
	assert.NoError(t, err, "it create a new machine")
	assert.NotNil(t, nd)
	assertTotalMachines(t, p, 1, "it creates one machine")

	_, nd2, err := p.Use(machineDefaultConfig, nil)
	assert.NoError(t, err, "it create a new machine")
	assert.NotNil(t, nd2)
	assertTotalMachines(t, p, 2, "it creates two machines")

	_, _, err = p.Use(machineProvisionFail, nil)
	assert.Error(t, err, "fail to create a new machine")
	assertTotalMachines(t, p, 2, "it fails to create a third machine")

	_, _, err = p.Use(machineNoConnect, nil)
	assert.Error(t, err, "fail to create a new machine on connect")
	assertTotalMachines(t, p, 3, "it fails on no-connect, but we leave the machine created")
}

func TestMachineCanConnectFailed(t *testing.T) {
	provisionRetryInterval = 0
	removalRetryInterval = 0
	useMachineRetryInterval = 0

	p, _ := testMachineProvider()
	d := p.machineDetails("no-connect", true)
	_, nd, err := p.Use(machineProvisionFail, d)
	assert.Error(t, err, "it fails to assign a machine")
	assert.Nil(t, nd)
	assert.Equal(t, machineStateIdle, d.State, "releases invalid machine")
}

func TestMachineUseCreatesMachine(t *testing.T) {
	provisionRetryInterval = 0
	removalRetryInterval = 0
	useMachineRetryInterval = 0

	p, _ := testMachineProvider()
	d := p.machineDetails("no-connect", true)
	_, nd, err := p.Use(machineDefaultConfig, d)
	assert.NoError(t, err, "it creates a new machine")
	assert.NotNil(t, nd)
	assert.Equal(t, machineStateIdle, d.State, "releases invalid machine")
}

func TestMachineUseFindsANewMachine(t *testing.T) {
	provisionRetryInterval = 0
	removalRetryInterval = 0
	useMachineRetryInterval = 0

	p, _ := testMachineProvider("machine2")
	d := p.machineDetails("no-connect", true)
	_, nd, err := p.Use(machineDefaultConfig, d)
	assert.NoError(t, err, "it find an existing machine")
	assert.NotNil(t, nd)
	d2 := p.machineDetails("machine2", false)
	assert.Equal(t, nd, d2, "it should be machine2")
	assert.Equal(t, machineStateIdle, d.State, "releases invalid machine")
}
