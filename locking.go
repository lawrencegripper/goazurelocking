package locking

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/Azure/azure-storage-blob-go/2016-05-31/azblob"
	"github.com/satori/go.uuid"
)

type (
	// Lock represents the status of a lock
	Lock struct {
		ctx           context.Context
		ready         sync.WaitGroup
		panic         func(string)
		LockTTL       time.Duration
		LockLost      chan struct{}
		LockID        uuid.UUID
		Lock          func() error
		Renew         func() error
		Unlock        func() error
		unlockContext func(context.Context) error
	}

	// BehaviorFunc is a type converter that allows a func to be used as a `Behavior`
	BehaviorFunc func(*Lock) *Lock
)

var (
	defaultLockBehaviors = []BehaviorFunc{AutoRenewLock, PanicOnLostLock, UnlockWhenContextCancelled}

	// AutoRenewLock configures the lock to autorenew itself
	AutoRenewLock = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			// Wait for the lock object to be ready
			l.ready.Wait()
			for {
				select {
				case <-time.Tick(l.LockTTL / 2):
					err := l.Renew()
					if err != nil {
						l.LockLost <- struct{}{}
						return
					}
				case <-l.LockLost:
					return
				case <-l.ctx.Done():
					return
				}
			}
		}()
		return l
	})

	// UnlockWhenContextCancelled will remove a lease when a context is cancelled
	UnlockWhenContextCancelled = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			// Wait for the lock object to be ready
			l.ready.Wait()
			for {
				select {
				case <-l.ctx.Done():
					l.unlockContext(context.Background()) //nolint: errcheck
				}
			}
		}()
		return l
	})

	// PanicOnLostLock configures the lock to autorenew itself
	PanicOnLostLock = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			// Wait for the lock object to be ready
			l.ready.Wait()

			select {
			case <-l.LockLost:
				l.panic("Lock lost and 'PanicOnLostLock' set")
			case <-l.ctx.Done():
				return
			}
		}()
		return l
	})
)

// NewLockInstance returns a new instance of a lock
func NewLockInstance(ctx context.Context, accountName, accountKey, lockName string, lockTTL time.Duration, behavior ...BehaviorFunc) (*Lock, error) {
	if accountKey == "" {
		return nil, fmt.Errorf("Empty accountKey is invalid")
	}
	if accountName == "" {
		return nil, fmt.Errorf("Empty accountName is invalid")
	}
	if lockTTL.Seconds() < 15 || lockTTL.Seconds() > 60 {
		return nil, fmt.Errorf("LockTTL of %v seconds is outside allowed range of 15-60seconds", lockTTL.Seconds())
	}

	// To create a child context to stop go routing leaks
	// do that here

	lockInstance := &Lock{
		ctx:      ctx,
		panic:    func(s string) { panic(s) },
		LockTTL:  lockTTL,
		LockLost: make(chan struct{}, 1),
		LockID:   uuid.NewV4(),
	}

	creds := azblob.NewSharedKeyCredential(accountName, accountKey)

	// Create a ContainerURL object to a container
	u, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s", accountName, lockName))
	containerURL := azblob.NewContainerURL(*u, azblob.NewPipeline(creds, azblob.PipelineOptions{}))
	_, err := containerURL.Create(ctx, nil, azblob.PublicAccessNone)

	// Create will return a '409' response code if the container already exists
	// we only error on other conditions as it's expected that a lock of this
	// name may already exist
	if err != nil && err.(azblob.ResponseError) != nil && err.(azblob.ResponseError).Response().StatusCode != 409 {
		return nil, err
	}

	lockInstance.Lock = func() error {
		_, err := containerURL.AcquireLease(lockInstance.ctx, lockInstance.LockID.String(), int32(lockTTL.Seconds()), azblob.HTTPAccessConditions{})
		if err != nil {
			return err
		}
		return nil
	}

	lockInstance.Renew = func() error {
		_, err := containerURL.RenewLease(lockInstance.ctx, lockInstance.LockID.String(), azblob.HTTPAccessConditions{})
		if err != nil {
			return err
		}
		return nil
	}

	lockInstance.unlockContext = func(ctx context.Context) error {
		_, err := containerURL.ReleaseLease(ctx, lockInstance.LockID.String(), azblob.HTTPAccessConditions{})
		if err != nil {
			return err
		}
		return nil
	}

	lockInstance.Unlock = func() error {
		return lockInstance.unlockContext(lockInstance.ctx)
	}

	// Hold off running behavior funcs until ready
	lockInstance.ready.Add(1)

	// If behaviors haven't been defined use the defaults
	if len(behavior) == 0 {
		behavior = defaultLockBehaviors
	}

	// Configure behaviors
	for _, b := range behavior {
		lockInstance = b(lockInstance)
	}

	// Start behavior funcs
	lockInstance.ready.Done()
	return lockInstance, nil
}
