package locking

import (
	"context"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/lawrencegripper/leaktest"
)

func TestLockingEnd2End_Simple(t *testing.T) {
	if testing.Short() {
		t.Log("Skipping integration test as '-short' specified")
		return
	}
	defer leaktest.Check(t)()

	randLockName := RandomName(10)

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, err := newLockTestHelper(ctx, randLockName, time.Duration(time.Second*15), AutoRenewLock)
	if err != nil {
		t.Error(err)
		return
	}

	err = lock.Lock()
	if err != nil {
		t.Errorf("Failed to get lock: %+v", err)
	}
	t.Logf("Acquired lease: %s", lock.LockID.String())

	// Attempt to take another lock of the same name
	duplicateLock, err := newLockTestHelper(ctx, randLockName, time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)
	if err != nil {
		t.Error(err)
		return
	}
	err = duplicateLock.Lock()
	if err == nil {
		t.Error("Expected an error but got nil when attempting to obtain already locked lock")
		return
	}

	// Release original lock
	err = lock.Unlock()
	if err != nil {
		t.Error(err)
		return
	}

	err = duplicateLock.Lock()
	if err != nil {
		t.Error("Expected duplicate lock to get lock but this failed")
		t.Error(err)
	}
}

func TestLockingEnd2End_UnlockOrRenewWithoutLocking(t *testing.T) {
	if testing.Short() {
		t.Log("Skipping integration test as '-short' specified")
		return
	}

	randLockName := RandomName(10)

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, err := newLockTestHelper(ctx, randLockName, time.Duration(time.Second*15), AutoRenewLock)
	if err != nil {
		t.Error(err)
		return
	}
	defer lock.Unlock()

	// Release original lock and check error is returned
	err = lock.Unlock()
	if err == nil || err.Error() != "Lock not acquired, can't unlock" {
		t.Errorf("Expected error to be returned when UNLOCKING as no lock is held")
		return
	}
	t.Logf("Error returned as expected: %+v", err)

	err = lock.Renew()
	if err == nil || err.Error() != "Lock not acquired, can't renew" {
		t.Errorf("Expected error to be returned when RENEWING as no lock is held")
		return
	}
	t.Logf("Error returned as expected: %+v", err)
}

func TestLockingEnd2End_Defaults(t *testing.T) {
	if testing.Short() {
		t.Log("Skipping integration test as '-short' specified")
		return
	}
	defer leaktest.Check(t)()

	randLockName := RandomName(10)

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, err := newLockTestHelper(ctx, randLockName, time.Duration(time.Second*15), defaultLockBehaviors...)
	if err != nil {
		t.Error(err)
		return
	}

	err = lock.Lock()
	if err != nil {
		t.Errorf("Failed to get lock: %+v", err)
	}
	t.Logf("Acquired lease: %s", lock.LockID.String())

	// Release original lock
	err = lock.Unlock()
	if err != nil {
		t.Error(err)
		return
	}

	// Attempt to take another lock of the same name
	duplicateLock, err := newLockTestHelper(ctx, randLockName, time.Duration(time.Second*15), defaultLockBehaviors...)
	if err != nil {
		t.Error(err)
		return
	}

	err = duplicateLock.Lock()
	if err != nil {
		t.Error("Expected duplicate lock to get lock but this failed")
	}

}

func TestLockingEnd2End_AutoRenewal(t *testing.T) {
	if testing.Short() {
		t.Log("Skipping integration test as '-short' specified")
		return
	}
	defer leaktest.Check(t)

	randLockName := RandomName(10)

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, err := newLockTestHelper(ctx, randLockName, time.Duration(time.Second*15), AutoRenewLock, PanicOnLostLock, UnlockWhenContextCancelled)
	if err != nil {
		t.Error(err)
		return
	}

	err = lock.Lock()
	if err != nil {
		t.Errorf("Failed to get lock: %+v", err)
	}
	t.Logf("Acquired lease: %s", lock.LockID.String())

	// Wait longer than the lock is valid for
	// so a renewal will have been necessary to prevent
	// the 'duplicatelock' acquiring a lock
	time.Sleep(time.Second * 20)

	// Attempt to take another lock of the same name
	duplicateLock, err := newLockTestHelper(ctx, randLockName, time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)
	if err != nil {
		t.Error(err)
		return
	}

	err = duplicateLock.Lock()
	if err == nil {
		t.Error("Expected an error but got nil when attempting to obtain already locked lock")
		return
	}
}

func TestLockingEnd2End_InvalidLockName(t *testing.T) {
	if testing.Short() {
		t.Log("Skipping integration test as '-short' specified")
		return
	}

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = newLockTestHelper(ctx, "lock1~~~@@@", time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)
	if err == nil {
		t.Error("Expected an error but got nil")
		return
	}

	t.Log(err)
}

func TestLockingEnd2End_UseLockTwice(t *testing.T) {
	if testing.Short() {
		t.Log("Skipping integration test as '-short' specified")
		return
	}

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, err := newLockTestHelper(ctx, "uselocktwice", time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)
	if err != nil {
		t.Error(err)
		return
	}

	err = lock.Lock()
	if err != nil {
		t.Errorf("Failed to get lock: %+v", err)
	}
	t.Logf("Acquired lease: %s", lock.LockID.String())

	err = lock.Unlock()
	if err != nil {
		t.Errorf("Failed to  unlock: %+v", err)
	}

	err = lock.Lock()
	if err == nil {
		t.Error("Expected error but got nil: should be able to reuse a lock instance")
	}
}

func TestLockingEnd2End_UseUnlockTwice(t *testing.T) {
	if testing.Short() {
		t.Log("Skipping integration test as '-short' specified")
		return
	}

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, err := newLockTestHelper(ctx, "useunlocktwice", time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)
	if err != nil {
		t.Error(err)
		return
	}

	err = lock.Lock()
	if err != nil {
		t.Errorf("Failed to get lock: %+v", err)
	}
	t.Logf("Acquired lease: %s", lock.LockID.String())

	err = lock.Unlock()
	if err != nil {
		t.Errorf("Failed to  unlock: %+v", err)
	}

	err = lock.Unlock()
	if err == nil {
		t.Error("Expected error but got nil: should be able to reuse a lock instance")
	}
}

func TestLockingEnd2End_LockRetry(t *testing.T) {
	if testing.Short() {
		t.Log("Skipping integration test as '-short' specified")
		return
	}
	defer leaktest.Check(t)

	randLockName := RandomName(10)

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Acquire a lock without autoRenew: will expire in 15 seconds
	lock, err := newLockTestHelper(ctx, randLockName, time.Duration(time.Second*15), UnlockWhenContextCancelled)
	if err != nil {
		t.Error(err)
		return
	}

	err = lock.Lock()
	if err != nil {
		t.Errorf("Failed to get lock: %+v", err)
	}
	t.Logf("Acquired lease: %s", lock.LockID.String())

	timeBeforeAttempts := time.Now()

	// Attempt to take another lock of the same name
	duplicateLock, err := newLockTestHelper(ctx, randLockName, time.Duration(time.Second*15), RetryObtainingLock, UnlockWhenContextCancelled)
	if err != nil {
		t.Error(err)
		return
	}

	// Expected that this lock operation will retry until it's acquired
	err = duplicateLock.Lock()
	if err != nil {
		t.Errorf("Expected lock to be acquired after retrys, got error: %v", err)
		return
	}

	// Check that this happened in a sensible timeframe
	timeAfterAttempts := time.Now()
	timeTakenToAcquireLock := timeAfterAttempts.Sub(timeBeforeAttempts).Seconds()

	if timeTakenToAcquireLock < 15 || timeTakenToAcquireLock > 30 {
		t.Errorf("Expected lock to be acquired in between 15-30, got: %v sec", timeTakenToAcquireLock)
	}
}

func newLockTestHelper(ctx context.Context, lockName string, lockTTL time.Duration, behavior ...BehaviorFunc) (*Lock, error) {
	accountName := os.Getenv("AZURE_STORAGE_NAME")
	accountKey := os.Getenv("AZURE_STORAGE_KEY")
	return NewLockInstance(ctx, accountName, accountKey, lockName, lockTTL, behavior...)
}

var lettersLower = []rune("abcdefghijklmnopqrstuvwxyz")

// RandomName random letter sequence
func RandomName(n int) string {
	return randFromSelection(n, lettersLower)
}

func randFromSelection(length int, choices []rune) string {
	b := make([]rune, length)
	rand.Seed(time.Now().UnixNano())
	for i := range b {
		b[i] = choices[rand.Intn(len(choices))]
	}
	return string(b)
}
