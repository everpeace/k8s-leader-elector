package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	crleaderelection "sigs.k8s.io/controller-runtime/pkg/leaderelection"
	"sigs.k8s.io/controller-runtime/pkg/recorder"
)

type Role string

const (
	RoleUnknown  Role = "Unknown"
	RoleLeader   Role = "Leader"
	RoleFollower Role = "Follower"
)

type SingleRoundLeaderElector struct {
	sync.RWMutex
	name               string
	namespace          string
	config             *rest.Config
	recorderProvider   recorder.Provider
	logger             logr.Logger
	taskCompletionChan chan error
	role               Role
	lock               resourcelock.Interface
	taskCmd            []string
	leaseDuration      time.Duration
	renewDeadline      time.Duration
	retryPeriod        time.Duration
}

func NewSingleRoundLeaderElector(
	config *rest.Config,
	logger logr.Logger,
	name string,
	namespace string,
	taskCmd []string,
	leaseDuration time.Duration,
	renewDeadline time.Duration,
	retryPeriod time.Duration) (*SingleRoundLeaderElector, error) {
	recorderProvider, err := NewRecorderProvider(config, scheme.Scheme, logger.WithName("recorder-provider"))
	if err != nil {
		return nil, err
	}
	return &SingleRoundLeaderElector{
		name:               name,
		namespace:          namespace,
		config:             config,
		recorderProvider:   recorderProvider,
		logger:             logger,
		taskCompletionChan: make(chan error),
		role:               RoleUnknown,
		taskCmd:            taskCmd,
		leaseDuration:      leaseDuration,
		renewDeadline:      renewDeadline,
		retryPeriod:        retryPeriod,
	}, nil
}
func (h *SingleRoundLeaderElector) doTask(ctx context.Context) {
	h.RLock()
	defer h.RUnlock()

	if len(h.taskCmd) <= 0 {
		h.logger.Info("no task specified. just sleeping forever.")
		return // it never completes.
	}

	var cmd *exec.Cmd
	if len(h.taskCmd) == 1 {
		cmd = exec.CommandContext(ctx, h.taskCmd[0])
	} else {
		cmd = exec.CommandContext(ctx, h.taskCmd[0], h.taskCmd[1:]...)
	}

	ler, err := h.lock.Get()
	if err != nil {
		msg := "error getting LeaderElectionRecord"
		h.logger.Error(err, msg)
		h.taskCompletionChan <- fmt.Errorf(msg)
		return
	}
	environs := []string{
		fmt.Sprintf("__LEADER_ELECTOR_ROLE=%s", h.role),
		fmt.Sprintf("__LEADER_ELECTOR_MY_IDENTITY=%s", h.lock.Identity()),
		fmt.Sprintf("__LEADER_ELECTOR_LEADER=%s", ler.HolderIdentity),
		fmt.Sprintf("__LEADER_ELECTOR_ACQUIRE_UNIXTIME=%d", ler.AcquireTime.Unix()),
	}
	env := append(os.Environ(), environs...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	h.logger.Info("running specified task", "command", h.taskCmd, "extra_environs", environs)
	if err := cmd.Start(); err != nil {
		h.taskCompletionChan <- err
	}
	result := cmd.Wait()
	if result == nil {
		h.logger.Info("task exited.", "code", 0)
	} else {
		if ee, ok := result.(*exec.ExitError); ok {
			h.logger.Info("task exited", "code", ee.ProcessState.ExitCode())
		} else {
			h.logger.Error(result, "error in running task")
		}
	}
	h.taskCompletionChan <- result
}

func (h *SingleRoundLeaderElector) startLeaderElection(ctx context.Context) error {
	myLock, err := crleaderelection.NewResourceLock(h.config, h.recorderProvider, crleaderelection.Options{
		LeaderElection:          true,
		LeaderElectionID:        h.name,
		LeaderElectionNamespace: h.namespace,
	})
	if err != nil {
		return err
	}
	h.lock = myLock

	h.logger.V(2).Info("my leader election identity", "identity", h.lock.Identity())
	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          myLock,
		LeaseDuration: h.leaseDuration,
		RenewDeadline: h.renewDeadline,
		RetryPeriod:   h.retryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ context.Context) {
				h.Lock()
				defer h.Unlock()
				if h.role == RoleLeader {
					// I'm a new leader.
					go h.doTask(ctx)
				} else {
					// Previous leader retired and I'm the successor.
					// this needs to stop leader election because this is single-round leader elector.
					h.taskCompletionChan <- nil
				}
			},
			OnNewLeader: func(identity string) {
				// This is called before OnStartedLeading callback.
				h.Lock()
				defer h.Unlock()
				if h.role == RoleUnknown {
					h.logger.V(2).Info("new leader observed", "identity", identity)
					// The first new leader observed.
					if identity == myLock.Identity() {
						h.logger.Info("I'm the leader", "leader", identity, "my_identity", myLock.Identity())
						// leader task will be actually invoked in OnStartedLeading.
						h.role = RoleLeader
					} else {
						ler, err := myLock.Get()
						if err != nil {
							h.logger.Error(err, "error getting LeaderElectionRecord")
							return
						}
						observedLockIsFresh := ler.RenewTime.Add(time.Duration(ler.LeaseDurationSeconds) * time.Second).After(time.Now())
						if observedLockIsFresh {
							// I'm a follower. initiating follower Task
							h.logger.Info("I'm a follower", "leader", identity, "my_identity", myLock.Identity())
							h.role = RoleFollower
							go h.doTask(ctx)
						} else {
							h.logger.V(2).Info("observed leader lease seems to be too old. ignoring to wait new leader election happens", "LeaderLeaseRecord", ler)
						}
					}
				} else {
					// leader was observed previously
					// skipping because this is single-round leader elector
					h.logger.V(2).Info("skipping this observation because my role has already been decided", "role", h.role)
				}
			},
			OnStoppedLeading: func() {
				// leader lease lost.  Something bad happen.
				// stopping leader elector
				h.taskCompletionChan <- fmt.Errorf("leader election lost")
			},
		},
	})
	if err != nil {
		return err
	}

	// Start the leader elector process
	go le.Run(ctx)
	return nil
}

func (h *SingleRoundLeaderElector) Start(stop <-chan struct{}) error {
	cancelable, cancel := context.WithCancel(context.Background())
	err := h.startLeaderElection(cancelable)
	if err != nil {
		cancel()
		return err
	}
	select {
	case <-stop:
		cancel()
		return nil
	case err := <-h.taskCompletionChan:
		cancel()
		return err
	}
}
