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

	"github.com/pkg/errors"
)

type Role string

const (
	RoleUnknown  Role = "Unknown"
	RoleLeader   Role = "Leader"
	RoleFollower Role = "Follower"
)

type SingleRoundLeaderElector struct {
	name               string
	namespace          string
	config             *rest.Config
	recorderProvider   recorder.Provider
	logger             logr.Logger
	taskCompletionChan chan error
	taskCmd            []string
	leaseDuration      time.Duration
	renewDeadline      time.Duration
	retryPeriod        time.Duration
	initialWait        bool
	resourceLock       resourcelock.Interface
	// This mutex protects 'role' and 'observedLeaderIdentity'
	sync.RWMutex
	role        Role
	observedLer *resourcelock.LeaderElectionRecord
}

func NewSingleRoundLeaderElector(
	config *rest.Config,
	logger logr.Logger,
	name string,
	namespace string,
	taskCmd []string,
	leaseDuration time.Duration,
	renewDeadline time.Duration,
	retryPeriod time.Duration,
	initialWait bool) (*SingleRoundLeaderElector, error) {
	recorderProvider, err := NewRecorderProvider(config, scheme.Scheme, logger.WithName("recorder-provider"))
	if err != nil {
		return nil, err
	}

	resourceLock, err := crleaderelection.NewResourceLock(config, recorderProvider, crleaderelection.Options{
		LeaderElection:          true,
		LeaderElectionID:        name,
		LeaderElectionNamespace: namespace,
	})
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
		resourceLock:       resourceLock,
		initialWait:        initialWait,
	}, nil
}

func (h *SingleRoundLeaderElector) startLeaderElection(ctx context.Context) error {
	h.logger.V(2).Info("my leader election identity", "identity", h.resourceLock.Identity())

	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          h.resourceLock,
		LeaseDuration: h.leaseDuration,
		RenewDeadline: h.renewDeadline,
		RetryPeriod:   h.retryPeriod,

		// callbacks notifies election state changes
		// - changes of leadership (not-leader <-> leader) can be detected in On[Started|Stopped]Leading
		// - onNewLeader should detect follower ship
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ctx context.Context) {
				h.Lock()
				defer h.Unlock()
				switch h.role {
				case RoleLeader, RoleUnknown:
					if h.role == RoleUnknown {
						h.role = RoleLeader
					}
					h.logger.Info("I'm elected as the leader", "newLeaderIdentity", h.myIdentity(), "myIdentity", h.myIdentity())
					go h.doTask(_ctx)
					return
				case RoleFollower:
					err := errors.New("another leader was newly elected (i.e. the current leadership was lost). this will exit because this is single round leader-elector")
					h.logger.Error(
						err, "I'm elected as the new leader although I was following to other leader",
						"newLeaderIdentity", h.myIdentity(),
						"previousLeaderIdentity", h.observedLer.HolderIdentity,
						"myIdentity", h.myIdentity(),
					)
					h.taskCompletionChan <- err
					return
				default:
					err := errors.New("callback=OnStartedLeading was called all though my role is invalid.")
					h.logger.Error(
						err, "this should never happen. maybe bug.",
						"role", h.role,
					)
					h.taskCompletionChan <- err
					return
				}
			},
			OnStoppedLeading: func() {
				h.RLock()
				defer h.RUnlock()
				switch h.role {
				case RoleLeader:
					// my leadership lost. stopping.
					h.taskCompletionChan <- errors.New("lost my leadership")
				case RoleUnknown:
					err := errors.New("leader election finished although my role hasn't decided")
					h.logger.Error(
						err, "callback=OnStartedLeading was called all though my role is Unknown.",
						"role", h.role,
					)
					h.taskCompletionChan <- err
					return
				default:
					err := errors.New(fmt.Sprintf("callback=OnStoppedLeading was called all though my role is %s", h.role))
					h.logger.Error(
						err, "this should never happen. maybe bug.",
						"role", h.role,
					)
					return
				}
			},
			OnNewLeader: func(newLeaderIdentity string) {
				h.Lock()
				defer h.Unlock()
				h.logger.V(2).Info("new leader observed.", "newLeaderIdentity", newLeaderIdentity)

				// my role hasn't decided yet
				ler, err := h.resourceLock.Get()
				if err != nil {
					msg := "error getting LeaderElectionRecord"
					h.logger.Error(err, msg)
					h.taskCompletionChan <- errors.Wrap(err, msg)
					return
				}

				if ler.HolderIdentity != newLeaderIdentity {
					h.logger.V(2).Info(
						"observed leader election record is inconsistent with observed new leader. waiting for next round.",
						"newLeaderIdentity", newLeaderIdentity, "leaderElectionRecord", ler,
					)
					return
				}

				switch h.role {
				case RoleUnknown:
					if ler.HolderIdentity == h.myIdentity() {
						h.role = RoleLeader
						h.observedLer = ler
						// task will handle in OnStartedLeading callback
						return
					}

					if h.observedLockIsFresh(ler) {
						h.logger.Info("I'm elected as a follower", "newLeaderIdentity", newLeaderIdentity, "myIdentity", h.myIdentity())
						h.role = RoleFollower
						h.observedLer = ler
						go h.doTask(ctx)
						return
					}
					// still unknown
					h.logger.V(2).Info(
						"observed leader election record seems to be too old. ignoring to wait new leader election happens",
						"myIdentity", h.myIdentity(), "LeaderElectionRecord", ler,
					)
					return
				case RoleFollower:
					if ler.HolderIdentity == h.myIdentity() {
						// If I'm escalated as new Leader, OnStartedLeading will be called.
						// So nothing to do here
						return
					}
					// new leadership detected as a follower
					if h.observedLockIsFresh(ler) {
						err := errors.New("current leader to whom I'm following lost their leadership.  this will exit because this is single round leader-elector")
						h.logger.Error(
							err, "I'm elected as a follower against another new leadership",
							"newLeaderIdentity", ler.HolderIdentity,
							"previousLeaderIdentity", h.observedLer.HolderIdentity,
							"myIdentity", h.myIdentity(),
						)
						h.taskCompletionChan <- err
						return
					}

					// observed not-fresh leader election record of another leader.  Does this happen??
					// If so, does follower task be stopped?
					h.logger.V(2).Info(
						"observed leader election record seems to be too old. ignoring.",
						"myIdentity", h.myIdentity(),
						"prerviousLeaderElectionRecord", h.observedLer,
						"observedLeaderElectionRecord", ler,
					)
					return
				case RoleLeader:
					// my leadership transitions will be handled in the other two callbacks.
					return
				default:
					h.logger.Error(
						errors.Errorf("OnNewLeader callback was called with role=%v", h.role),
						"this should never happen",
					)
					return
				}
			},
		},
	})
	if err != nil {
		return err
	}

	if h.initialWait {
		h.logger.V(2).Info("wait for the old lease being expired if no leader exist",
			"duration(=lease-duration+renew-deadline)", (h.leaseDuration + h.renewDeadline).String(),
		)
		time.Sleep(h.leaseDuration + h.renewDeadline)
	}

	// Start the leader election
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

func (h *SingleRoundLeaderElector) observedLockIsFresh(ler *resourcelock.LeaderElectionRecord) bool {
	return time.Now().Before(ler.RenewTime.Add(time.Duration(ler.LeaseDurationSeconds) * time.Second))
}

func (h *SingleRoundLeaderElector) myIdentity() string {
	return h.resourceLock.Identity()
}

func (h *SingleRoundLeaderElector) doTask(ctx context.Context) {
	environs := []string{
		fmt.Sprintf("__K8S_LEADER_ELECTOR_ROLE=%s", h.role),
		fmt.Sprintf("__K8S_LEADER_ELECTOR_MY_IDENTITY=%s", h.resourceLock.Identity()),
		fmt.Sprintf("__K8S_LEADER_ELECTOR_LEADER_IDENTITY=%s", h.observedLer.HolderIdentity),
		fmt.Sprintf("__K8S_LEADER_ELECTOR_ACQUIRE_TIME_RFC3339=%s", h.observedLer.AcquireTime.Format(time.RFC3339)),
		fmt.Sprintf("__K8S_LEADER_ELECTOR_RENEW_TIME_RFC3339=%s", h.observedLer.RenewTime.Format(time.RFC3339)),
		fmt.Sprintf("__K8S_LEADER_ELECTOR_LEADER_TRANSITIONS=%d", h.observedLer.LeaderTransitions),
	}
	cmdEnv := append(os.Environ(), environs...)

	if len(h.taskCmd) <= 0 {
		h.logger.Info("no task is specified.  just printing leader election results as environment variables and then sleep until losing leadership.", "environs", environs)
		for _, environ := range environs {
			fmt.Printf("export %s\n", environ)
		}
		return
	}

	var cmd *exec.Cmd
	if len(h.taskCmd) == 1 {
		cmd = exec.CommandContext(ctx, h.taskCmd[0])
	} else {
		cmd = exec.CommandContext(ctx, h.taskCmd[0], h.taskCmd[1:]...)
	}
	cmd.Env = cmdEnv
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
