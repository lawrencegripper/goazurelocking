package locking

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-storage-blob-go/2016-05-31/azblob"
	"github.com/cenkalti/backoff"
	"github.com/satori/go.uuid"
)

const lockContainerNamePrefix = "azlk-" // This is appended to the blob containers created by the library

type (
	// Lock represents the status of a lock
	Lock struct {
		ctx              context.Context
		used             bool                        // Set to True when a lock has been unlocked
		lockAcquired     bool                        // Set to True when a lock has been acquired
		panic            func(string)                // Used for testing to allow panic call to be mocked
		unlockContext    func(context.Context) error // Used by 'UnlockWhenCancelled' behavior to pass temporary context to unlock
		cleanupContainer func(context.Context)       // Used to cleanup the Azure Blob container after a lock is unlocked

		// Cancel calls the 'Unlock' method but, when combined with the 'UnlockWhenContextCancelled' behavior, will use a temporary
		// context will a 5 second deadline to allow the lock to be released even if the parent context has been cancelled.
		Cancel context.CancelFunc

		// LockTTL is the duration for which the lock is to be held
		// Valid options: 15sec -> 60sec due to Azure Blob https://docs.microsoft.com/en-us/rest/api/storageservices/lease-container
		LockTTL time.Duration

		// LockLost This channel is signaled by the 'AutoRenew' behavior if the lock is lost
		LockLost chan struct{}

		// LockID is the ID of the underlying blob lease
		LockID uuid.UUID

		// Lock will acquire a lock for the specified name
		Lock func() error

		// Renew will renew the lock, if present
		// or return an error if no lock is held
		Renew func() error

		// Unlock will release the lock, if present
		// or return an error if no lock is held
		Unlock func() error
	}

	// BehaviorFunc is a type converter that allows a func to be used as a `Behavior`
	BehaviorFunc func(*Lock) *Lock
)

var (
	defaultLockBehaviors = []BehaviorFunc{AutoRenewLock, PanicOnLostLock, UnlockWhenContextCancelled, RetryObtainingLock}

	// AutoRenewLock configures the lock to autorenew itself
	AutoRenewLock = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			for {
				select {
				case <-l.ctx.Done():
					// Context has been cancelled, exit so can be gc'd
					return
				case <-time.Tick(l.LockTTL / 2):
					// If the 'lock' function hasn't been used yet spin
					if !l.lockAcquired {
						continue
					}
					// Do a renew. If we fail, clean up and notify that the lock is lost
					err := l.Renew()
					if err != nil {
						l.Cancel()
						l.LockLost <- struct{}{}
						return
					}
				case <-l.LockLost:
					return
				}
			}
		}()
		return l
	})

	// RetryObtainingLock configures the lock to retry getting a lock if it is already held
	RetryObtainingLock = BehaviorFunc(func(l *Lock) *Lock {
		// Assuming locks will be initialised with roughly
		// the correct TTL required to perform the operation
		// lets give it 10x time to acquire it
		obtainLockBackoffPolicy := backoff.NewExponentialBackOff()
		obtainLockBackoffPolicy.MaxElapsedTime = l.LockTTL * 10
		existingLockFunc := l.Lock

		// Replace existing lock function with exponential retrying one
		l.Lock = func() error { return backoff.Retry(existingLockFunc, obtainLockBackoffPolicy) }

		return l
	})

	// UnlockWhenContextCancelled will remove a lease when a context is cancelled
	UnlockWhenContextCancelled = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			for {
				select {
				case <-l.ctx.Done():
					// If the 'lock' function wasn't ever called don't worry
					if !l.lockAcquired {
						return
					}
					// The original context is dead but we don't want to leave the lock in place
					// so lets create a new context and give it 3 seconds to get the job done
					ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second*5))
					defer cancel()
					l.unlockContext(ctx) //nolint: errcheck

					return
				case <-l.LockLost:
					return
				}
			}
		}()
		return l
	})

	// PanicOnLostLock configures the lock to autorenew itself
	PanicOnLostLock = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			select {
			case <-l.ctx.Done():
				return
			case <-l.LockLost:
				l.Cancel()
				l.panic("Lock lost and 'PanicOnLostLock' set")
			}
		}()
		return l
	})
)

// NewLockInstance returns a new instance of a lock
func NewLockInstance(ctxParent context.Context, accountName, accountKey, lockName string, lockTTL time.Duration, behavior ...BehaviorFunc) (*Lock, error) {
	if accountKey == "" {
		return nil, fmt.Errorf("Empty accountKey is invalid")
	}
	if accountName == "" {
		return nil, fmt.Errorf("Empty accountName is invalid")
	}
	if lockTTL.Seconds() < 15 || lockTTL.Seconds() > 60 {
		return nil, fmt.Errorf("LockTTL of %v seconds is outside allowed range of 15-60seconds", lockTTL.Seconds())
	}
	if valid, err := IsValidLockName(lockName); !valid {
		return nil, err
	}

	creds := azblob.NewSharedKeyCredential(accountName, accountKey)

	// Create a ContainerURL object to a container
	lockName = strings.ToLower(lockName)
	u, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s", accountName, lockContainerNamePrefix+lockName))
	containerURL := azblob.NewContainerURL(*u, azblob.NewPipeline(creds, azblob.PipelineOptions{}))
	_, err := containerURL.Create(ctxParent, nil, azblob.PublicAccessNone)

	// Create will return a '409' response code if the container already exists
	// we only error on other conditions as it's expected that a lock of this
	// name may already exist
	if err != nil && err.(azblob.ResponseError) != nil && err.(azblob.ResponseError).Response().StatusCode != 409 {
		return nil, err
	}

	// Create our own context which will be cancelled independently of
	// the parent context
	ctx, cancel := context.WithCancel(ctxParent)

	lockInstance := &Lock{
		ctx:      ctx,
		Cancel:   cancel,
		panic:    func(s string) { panic(s) },
		LockTTL:  lockTTL,
		LockLost: make(chan struct{}, 1),
		LockID:   uuid.NewV4(),
	}

	// This function is used to cleanup the container
	// created by the lock instance. It's called after an
	// instance has 'Unlock' called. It's a best effort measure
	// in some circumstances containers will be left in the storage account
	lockInstance.cleanupContainer = func(ctx context.Context) {
		containerURL.Delete(ctx, azblob.ContainerAccessConditions{}) //nolint: errcheck
	}

	// This function handles unlocking
	// it accepts a context to allow locks to be unlocked
	// even after a context has been cancelled
	lockInstance.unlockContext = func(ctx context.Context) error {
		if !lockInstance.lockAcquired {
			return fmt.Errorf("Lock not acquired, can't unlock")
		}

		// Mark this lock instance as used to prevent reuse
		// as the library doesn't handle multiple uses per lock instance
		lockInstance.used = true

		// No matter what happened cancel the context to close off the go routines running in behaviors
		defer lockInstance.Cancel()

		_, err := containerURL.ReleaseLease(ctx, lockInstance.LockID.String(), azblob.HTTPAccessConditions{})
		lockInstance.cleanupContainer(ctx)

		if err != nil {
			return err
		}

		return nil
	}

	lockInstance.Unlock = func() error {
		return lockInstance.unlockContext(lockInstance.ctx)
	}

	lockInstance.Lock = func() error {
		if lockInstance.used {
			return fmt.Errorf("Lock instance already unlocked, cannot be reused")
		}
		if lockInstance.lockAcquired {
			return fmt.Errorf("Lock already acquire, call 'renew' to extend a lock")
		}
		_, err := containerURL.AcquireLease(lockInstance.ctx, lockInstance.LockID.String(), int32(lockTTL.Seconds()), azblob.HTTPAccessConditions{})
		if err != nil {
			return err
		}

		lockInstance.lockAcquired = true

		return nil
	}

	lockInstance.Renew = func() error {
		if !lockInstance.lockAcquired {
			return fmt.Errorf("Lock not acquired, can't renew")
		}
		if lockInstance.used {
			return fmt.Errorf("Lock instance already used, cannot be reused")
		}
		_, err := containerURL.RenewLease(lockInstance.ctx, lockInstance.LockID.String(), azblob.HTTPAccessConditions{})
		if err != nil {
			return err
		}
		return nil
	}

	// If behaviors haven't been defined use the defaults
	if len(behavior) == 0 {
		behavior = defaultLockBehaviors
	}

	// Configure behaviors
	for _, b := range behavior {
		lockInstance = b(lockInstance)
	}

	return lockInstance, nil
}

// validLockNameRegex is a regex used to check the chars are valid as an Azure Storage container name
var validLockNameRegex = regexp.MustCompile("^[a-z0-9]+(-[a-z0-9]+)*$")

// IsValidLockName checks if the lock name is between 3-58 (63 minus 5char prefix used) characters long
// and matches this regex @"^[a-z0-9]+(-[a-z0-9]+)*$"
func IsValidLockName(lockName string) (bool, error) {
	lockName = strings.ToLower(lockName)
	if len(lockName) < 3 || len(lockName) > 58 {
		return false, fmt.Errorf("lock name: %s must be between 3 and 58 characters long", lockName)
	}

	if !validLockNameRegex.MatchString(lockName) {
		return false, fmt.Errorf("lock name: %s must be alphanumberic with no characters other than '-' (regex '^[a-z0-9]+(-[a-z0-9]+)*$')", lockName)
	}

	return true, nil
}
