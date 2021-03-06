package locking

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lawrencegripper/leaktest"
)

func TestAutoRenewLockBehavior_Normal(t *testing.T) {
	defer leaktest.Check(t)()

	renewCalledcount := 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lockInstance := &Lock{
		ctx:          ctx,
		lockAcquired: true,
		LockTTL:      time.Duration(time.Second * 1),
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
	defer leaktest.Check(t)()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctxWasCancelled := false
	lockInstance := &Lock{
		ctx:          ctx,
		lockAcquired: true,
		cancel: func() {
			ctxWasCancelled = true
		},
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

	if !ctxWasCancelled {
		t.Error("Expect ctx to be cancelled")
	}
}

func TestRetryObtainingLockBehavior(t *testing.T) {
	defer leaktest.Check(t)()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shouldAccept := false
	lockAttempts := 0
	go func() {
		time.Sleep(time.Second * 3)
		shouldAccept = true
	}()

	lockInstance := &Lock{
		ctx:     ctx,
		LockTTL: time.Duration(time.Second * 1),
		Lock: func() error {
			if shouldAccept {
				return nil
			}
			lockAttempts++
			return fmt.Errorf("Simulated error")
		},
		LockLost: make(chan struct{}, 1),
	}

	RetryObtainingLock(lockInstance)

	err := lockInstance.Lock()
	if err != nil {
		t.Error("Failed to retry lock and instead returned error")
	}

	t.Logf("Lock obtained after %v attempts", lockAttempts)
}

func TestPanicOnLostLock(t *testing.T) {
	defer leaktest.Check(t)()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	unlockWasCalled := false
	didPanic := false
	lockInstance := &Lock{
		ctx: ctx,
		Unlock: func() error {
			unlockWasCalled = true
			return nil
		},
		panic:    func(s string) { didPanic = true },
		LockTTL:  time.Duration(time.Second * 1),
		LockLost: make(chan struct{}, 1),
	}

	lockInstance.LockLost <- struct{}{}
	PanicOnLostLock(lockInstance)

	// Allow the go routine time to run
	time.Sleep(time.Millisecond * 10)

	// Because the panic is in the goroutine we can't detect it easily in
	// the test so a panic func is used for this purpose

	if !didPanic {
		t.Error("Expected panic and didn't get one")
	}

	if !unlockWasCalled {
		t.Error("Expect `unlock` to be called")
	}
}

func TestIsValidLockName(t *testing.T) {

	tests := []struct {
		name     string
		lockName string
		want     bool
		wantErr  bool
	}{
		{
			name:     "ValidLockName1",
			lockName: "validlock1",
			want:     true,
			wantErr:  false,
		},
		{
			name:     "ValidLockName2_toLowerCaps",
			lockName: "ValidLock1",
			want:     true,
			wantErr:  false,
		},
		{
			name:     "InvalidLockName_Long",
			lockName: "invalidlock1validlock1validlock1validlock1validlock1validlock",
			want:     false,
			wantErr:  true,
		},
		{
			name:     "InvalidLockName_@",
			lockName: "invalid@lock",
			want:     false,
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IsValidLockName(tt.lockName)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsValidLockName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("IsValidLockName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInvalidStorageUrl_WithPath(t *testing.T) {
	randLockName := RandomName(10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := NewLockInstance(ctx,
		"https://storageaccount.com/someinvalidpath", "somekey",
		randLockName, time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)

	if err == nil {
		t.Error("Expected an error but got nil")
		return
	}

	t.Log(err)
}

func TestInvalidStorageUrl_WithoutHTTP(t *testing.T) {
	randLockName := RandomName(10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := NewLockInstance(ctx,
		"https://storageaccount.com/someinvalidpath", "somekey",
		randLockName, time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)

	if err == nil {
		t.Error("Expected an error but got nil")
		return
	}

	t.Log(err)
}
