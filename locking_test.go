package locking

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAutoRenewLockBehavior_Normal(t *testing.T) {
	renewCalledcount := 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lockInstance := &Lock{
		ctx:     ctx,
		LockTTL: time.Duration(time.Second * 1),
		Renew: func() error {
			renewCalledcount++
			return nil
		},
	}

	AutoRenewLock(lockInstance)

	time.Sleep(time.Second * 6)

	if renewCalledcount < 10 && renewCalledcount < 12 {
		t.Errorf("Lock renewal failed. Expected: 10-12 renewals Got: %v", renewCalledcount)
	}
}

func TestAutoRenewLockBehavior_Fail(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lockInstance := &Lock{
		ctx:     ctx,
		LockTTL: time.Duration(time.Second * 1),
		Renew: func() error {
			return fmt.Errorf("Simulated error")
		},
		LockLost: make(chan struct{}, 1),
	}

	AutoRenewLock(lockInstance)

	select {
	case <-lockInstance.LockLost:
		t.Log("Lock lost signaled as expected")
	case <-time.Tick(time.Second * 2):
		t.Error("Lock lost NOT sent after 2 seconds")
	}
}
