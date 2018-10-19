package locking

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-storage-blob-go/2016-05-31/azblob"
	"github.com/fortytw2/leaktest"
	"github.com/joho/godotenv"
)

func TestLockingEnd2End_Simple(t *testing.T) {
	if testing.Short() {
		t.Log("Skipping integration test as '-short' specified")
		return
	}
	defer leaktest.Check(t)()

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, err := NewLockTestHelper(ctx, "Simple", time.Duration(time.Second*15), AutoRenewLock)
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
	duplicateLock, err := NewLockTestHelper(ctx, "Simple", time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)
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
	}
}

func TestLockingEnd2End_UnlockOrRenewWithoutLocking(t *testing.T) {
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

	lock, err := NewLockTestHelper(ctx, "simple", time.Duration(time.Second*15), AutoRenewLock)
	if err != nil {
		t.Error(err)
		return
	}
	defer lock.Cancel()

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

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, err := NewLockTestHelper(ctx, "defaults", time.Duration(time.Second*15), defaultLockBehaviors...)
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
	duplicateLock, err := NewLockTestHelper(ctx, "defaults", time.Duration(time.Second*15), defaultLockBehaviors...)
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
	defer leaktest.CheckTimeout(t, time.Duration(time.Second*15))()

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, err := NewLockTestHelper(ctx, "lock2", time.Duration(time.Second*15), AutoRenewLock, PanicOnLostLock, UnlockWhenContextCancelled)
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
	duplicateLock, err := NewLockTestHelper(ctx, "lock2", time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)
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

	_, err = NewLockTestHelper(ctx, "lock1~~~@@@", time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)
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

	lock, err := NewLockTestHelper(ctx, "uselocktwice", time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)
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

	lock, err := NewLockTestHelper(ctx, "useunlocktwice", time.Duration(time.Second*15), AutoRenewLock, UnlockWhenContextCancelled)
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

	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Acquire a lock without autoRenew: will expire in 15 seconds
	lock, err := NewLockTestHelper(ctx, "lockretry", time.Duration(time.Second*15), UnlockWhenContextCancelled)
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
	duplicateLock, err := NewLockTestHelper(ctx, "lockretry", time.Duration(time.Second*15), RetryObtainingLock, UnlockWhenContextCancelled)
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

func TestLockingEnd2End_CheckContainerCleanup(t *testing.T) {
	if testing.Short() {
		t.Log("Skipping integration test as '-short' specified")
		return
	}

	lockName := strings.ToLower("CheckCleanup")
	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, err := NewLockTestHelper(ctx, lockName, time.Duration(time.Second*15), AutoRenewLock)
	if err != nil {
		t.Error(err)
		return
	}
	defer lock.Cancel()

	err = lock.Lock()
	if err != nil {
		t.Errorf("Failed acquiring lock: %v", err)
	}

	// Release original lock and check error is returned
	err = lock.Unlock()
	if err != nil {
		t.Errorf("Expected error to be returned when UNLOCKING as no lock is held")
		return
	}

	// Check to see the container has been removed
	accountName := os.Getenv("AZURE_STORAGE_NAME")
	accountKey := os.Getenv("AZURE_STORAGE_KEY")

	creds := azblob.NewSharedKeyCredential(accountName, accountKey)

	// Create a ContainerURL object to a container
	u, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s", accountName, lockContainerNamePrefix+lockName))
	containerURL := azblob.NewContainerURL(*u, azblob.NewPipeline(creds, azblob.PipelineOptions{}))

	props, err := containerURL.GetPropertiesAndMetadata(ctx, azblob.LeaseAccessConditions{})
	if err == nil && props.StatusCode() == 200 {
		t.Error("Expected container to be removed but still present")
	}

}

func NewLockTestHelper(ctx context.Context, lockName string, lockTTL time.Duration, behavior ...BehaviorFunc) (*Lock, error) {
	accountName := os.Getenv("AZURE_STORAGE_NAME")
	accountKey := os.Getenv("AZURE_STORAGE_KEY")
	return NewLockInstance(ctx, accountName, accountKey, lockName, lockTTL, behavior...)
}
