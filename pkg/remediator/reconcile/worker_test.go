// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package reconcile

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/GoogleContainerTools/config-sync/pkg/api/configsync"
	"github.com/GoogleContainerTools/config-sync/pkg/core"
	"github.com/GoogleContainerTools/config-sync/pkg/core/k8sobjects"
	"github.com/GoogleContainerTools/config-sync/pkg/declared"
	"github.com/GoogleContainerTools/config-sync/pkg/kinds"
	"github.com/GoogleContainerTools/config-sync/pkg/metadata"
	"github.com/GoogleContainerTools/config-sync/pkg/remediator/queue"
	"github.com/GoogleContainerTools/config-sync/pkg/status"
	"github.com/GoogleContainerTools/config-sync/pkg/syncer/syncertest"
	syncertestfake "github.com/GoogleContainerTools/config-sync/pkg/syncer/syncertest/fake"
	testingfake "github.com/GoogleContainerTools/config-sync/pkg/syncer/syncertest/fake"
	"github.com/GoogleContainerTools/config-sync/pkg/testing/testerrors"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	v1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestWorker_Run_Remediates verifies that worker.Run remediates declared
// objects added to the queue.
func TestWorker_Run_Remediates(t *testing.T) {
	testCases := []struct {
		name         string
		existingObjs []client.Object
		declaredObjs []client.Object
		changedObjs  []client.Object
		eventObjs    []client.Object
		expectedObjs []client.Object
	}{
		{
			name: "revert delete",
			existingObjs: []client.Object{
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled),
			},
			declaredObjs: []client.Object{
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled),
			},
			changedObjs: []client.Object{
				queue.MarkDeleted(context.Background(), k8sobjects.ClusterRoleBindingObject()),
			},
			eventObjs: []client.Object{
				queue.MarkDeleted(context.Background(), k8sobjects.ClusterRoleBindingObject()),
			},
			expectedObjs: []client.Object{
				// TODO: Upgrade FakeClient to increment UID after deletion
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
					core.UID("1"), core.ResourceVersion("1"), core.Generation(1),
				),
			},
		},
		{
			name: "revert watch filter label removal",
			existingObjs: []client.Object{
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
					core.Label("example-label", "example-value")),
			},
			declaredObjs: []client.Object{
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
					core.Label("example-label", "example-value")),
			},
			changedObjs: []client.Object{
				// Update object to remove label
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled),
			},
			eventObjs: []client.Object{
				// Watch server treats update to remove required label as a delete.
				// Delete event includes previous object state.
				queue.MarkDeleted(context.Background(),
					k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
						core.Label("example-label", "example-value"))),
			},
			expectedObjs: []client.Object{
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
					core.Label("example-label", "example-value"),
					core.UID("1"), core.ResourceVersion("3"), core.Generation(1),
				),
			},
		},
		{
			name: "revert update",
			existingObjs: []client.Object{
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled),
			},
			declaredObjs: []client.Object{
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled),
			},
			changedObjs: []client.Object{
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
					core.Label("new", "label")),
			},
			eventObjs: []client.Object{
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
					core.Label("new", "label")),
			},
			expectedObjs: []client.Object{
				// Role change should be reverted
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
					core.UID("1"), core.ResourceVersion("3"), core.Generation(1),
				),
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			q := queue.New("test")
			defer q.ShutDown()

			c := testingfake.NewClient(t, core.Scheme, tc.existingObjs...)

			d := makeDeclared(t, randomCommitHash(), tc.declaredObjs...)
			w := NewWorker(declared.RootScope, configsync.RootSyncName, c.Applier(configsync.FieldManager), q, d,
				syncertestfake.NewConflictHandler(), syncertestfake.NewFightHandler())

			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			// Run worker in the background
			doneCh := make(chan struct{})
			go func() {
				defer close(doneCh)
				w.Run(ctx)
			}()

			// Execute runtime changes
			for _, obj := range tc.changedObjs {
				if deletedObj, ok := obj.(*queue.Deleted); ok {
					if err := c.Delete(ctx, deletedObj.Object); err != nil {
						t.Fatalf("Failed to delete object in fake client: %v", err)
					}
				} else {
					if err := c.Update(ctx, obj, client.FieldOwner(testingfake.FieldManager)); err != nil {
						t.Fatalf("Failed to update object in fake client: %v", err)
					}
				}
			}

			// Simulate watch events to add the objects to the queue
			for _, obj := range tc.eventObjs {
				q.Add(obj)
			}

			// Give the worker a few seconds to remediate
			// TODO: use client.Watch to watch for the desired changes (requires FakeClient to impl Watch).
			time.Sleep(2 * time.Second)
			cancel()

			// Wait for worker to exit or timeout
			timeout := time.NewTimer(5 * time.Second)
			defer timeout.Stop()
			select {
			case <-timeout.C:
				// fail
				t.Error("Run() failed to return when context was cancelled")
			case <-doneCh:
				// pass
				c.Check(t, tc.expectedObjs...)
			}
		})
	}
}

// TestWorker_Run_RemediatesExisting verifies that worker.Run remediates declared
// objects from a queue populated before the Worker started.
func TestWorker_Run_RemediatesExisting(t *testing.T) {
	ctx := context.Background()

	existingObjs := []client.Object{
		k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled),
		k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled),
	}
	declaredObjs := []client.Object{
		k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled),
		k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled),
	}
	changedObjs := []client.Object{
		queue.MarkDeleted(ctx, k8sobjects.ClusterRoleBindingObject()),
		k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
			core.Label("new", "label")),
	}
	expectedObjs := []client.Object{
		// CRB delete should be reverted
		// TODO: Upgrade FakeClient to increment UID after deletion
		k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
			core.UID("1"), core.ResourceVersion("1"), core.Generation(1),
		),
		// Role change should be reverted
		k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
			core.UID("1"), core.ResourceVersion("3"), core.Generation(1),
		),
	}

	q := queue.New("test")
	defer q.ShutDown()

	c := testingfake.NewClient(t, core.Scheme, existingObjs...)

	// Execute runtime changes
	for _, obj := range changedObjs {
		if deletedObj, ok := obj.(*queue.Deleted); ok {
			if err := c.Delete(ctx, deletedObj.Object); err != nil {
				t.Fatalf("Failed to delete object in fake client: %v", err)
			}
		} else {
			if err := c.Update(ctx, obj, client.FieldOwner(testingfake.FieldManager)); err != nil {
				t.Fatalf("Failed to update object in fake client: %v", err)
			}
		}
	}

	// Simulate watch events to add the objects to the queue
	for _, obj := range changedObjs {
		q.Add(obj)
	}

	d := makeDeclared(t, randomCommitHash(), declaredObjs...)
	w := NewWorker(declared.RootScope, configsync.RootSyncName, c.Applier(configsync.FieldManager), q, d,
		syncertestfake.NewConflictHandler(), syncertestfake.NewFightHandler())

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Run worker in the background
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		w.Run(ctx)
	}()

	// Give the worker a few seconds to remediate
	// TODO: use client.Watch to watch for the desired changes (requires FakeClient to impl Watch).
	time.Sleep(2 * time.Second)
	cancel()

	// Wait for worker to exit or timeout
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	select {
	case <-timeout.C:
		// fail
		t.Error("Run() failed to return when context was cancelled")
	case <-doneCh:
		// pass
		c.Check(t, expectedObjs...)
	}
}

func TestWorker_ProcessNextObject(t *testing.T) {
	testCases := []struct {
		name      string
		declared  []client.Object
		toProcess []client.Object
		want      []client.Object
	}{
		{
			name: "update actual objects",
			declared: []client.Object{
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
					core.Label("first", "one")),
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
					core.Label("second", "two")),
			},
			toProcess: []client.Object{
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled),
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled),
			},
			want: []client.Object{
				// TODO: Figure out why the reconciler is stripping away labels and annotations.
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
					core.UID("1"), core.ResourceVersion("2"), core.Generation(1),
					core.Label("first", "one")),
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
					core.UID("1"), core.ResourceVersion("2"), core.Generation(1),
					core.Label("second", "two")),
			},
		},
		{
			name:     "delete undeclared objects",
			declared: []client.Object{},
			toProcess: []client.Object{
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_clusterrolebinding_default-name")),
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
					core.Annotation(metadata.ResourceIDKey, "rbac.authorization.k8s.io_clusterrole_default-name")),
			},
			want: []client.Object{},
		},
		{
			name: "create missing objects",
			declared: []client.Object{
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled),
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled),
			},
			toProcess: []client.Object{
				queue.MarkDeleted(context.Background(), k8sobjects.ClusterRoleBindingObject()),
				queue.MarkDeleted(context.Background(), k8sobjects.ClusterRoleObject()),
			},
			want: []client.Object{
				k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
					core.UID("1"), core.ResourceVersion("1"), core.Generation(1),
				),
				k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
					core.UID("1"), core.ResourceVersion("1"), core.Generation(1),
				),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			q := queue.New("test")
			for _, obj := range tc.toProcess {
				q.Add(obj)
			}

			c := testingfake.NewClient(t, core.Scheme)
			for _, obj := range tc.toProcess {
				if !queue.WasDeleted(context.Background(), obj) {
					if err := c.Create(context.Background(), obj, client.FieldOwner(testingfake.FieldManager)); err != nil {
						t.Fatalf("Failed to create object in fake client: %v", err)
					}
				}
			}

			d := makeDeclared(t, randomCommitHash(), tc.declared...)
			w := NewWorker(declared.RootScope, configsync.RootSyncName, c.Applier(configsync.FieldManager), q, d,
				syncertestfake.NewConflictHandler(), syncertestfake.NewFightHandler())

			for _, obj := range tc.toProcess {
				if err := w.processNextObject(context.Background()); err != nil {
					t.Errorf("unexpected error from processNextObject() for object %q: %v", core.IDOf(obj), err)
				}
			}

			c.Check(t, tc.want...)
		})
	}
}

// TestWorker_Run_Cancelled verifies that worker.Run can be cancelled when the
// queue is empty and shut down.
func TestWorker_Run_CancelledWhenEmpty(t *testing.T) {
	q := queue.New("test") // empty queue
	defer q.ShutDown()
	c := testingfake.NewClient(t, core.Scheme)
	d := makeDeclared(t, randomCommitHash()) // no resources declared
	w := NewWorker(declared.RootScope, configsync.RootSyncName, c.Applier(configsync.FieldManager), q, d,
		syncertestfake.NewConflictHandler(), syncertestfake.NewFightHandler())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run worker in the background
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		w.Run(ctx)
	}()

	// Let the worker run for a bit and then stop it.
	time.Sleep(1 * time.Second)
	cancel()

	// Wait for worker to exit or timeout
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	select {
	case <-timeout.C:
		// fail
		t.Error("Run() with empty queue did not return when context was cancelled")
	case <-doneCh:
		// pass
		c.Check(t) // no objects expected
	}
}

// TestWorker_Run_CancelledWhenNotEmpty verifies that worker.Run can be
// cancelled when the queue is not empty.
// Use a fake client Update error to prevent the queue from draining.
func TestWorker_Run_CancelledWhenNotEmpty(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existingObjs := []client.Object{
		k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled),
		k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled),
	}
	declaredObjs := []client.Object{
		k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled),
		k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled),
	}
	changedObjs := []client.Object{
		queue.MarkDeleted(ctx, k8sobjects.ClusterRoleBindingObject()),
		k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
			core.Label("new", "label")),
	}
	expectedObjs := []client.Object{
		// CRB delete should be reverted
		k8sobjects.ClusterRoleBindingObject(syncertest.ManagementEnabled,
			core.UID("1"), core.ResourceVersion("1"), core.Generation(1),
		),
		// Role revert should fail from fake Update error
		k8sobjects.ClusterRoleObject(syncertest.ManagementEnabled,
			core.UID("1"), core.ResourceVersion("2"), core.Generation(1),
			core.Label("new", "label"),
		),
	}

	q := queue.New("test")
	defer q.ShutDown()

	c := testingfake.NewClient(t, core.Scheme, existingObjs...)
	d := makeDeclared(t, randomCommitHash(), declaredObjs...)
	a := &testingfake.Applier{Client: c, FieldManager: configsync.FieldManager}
	w := NewWorker(declared.RootScope, configsync.RootSyncName, a, q, d,
		syncertestfake.NewConflictHandler(), syncertestfake.NewFightHandler())

	// Run worker in the background
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		w.Run(ctx)
	}()

	// Run the worker for a bit with an empty queue, to make sure it starts up.
	time.Sleep(1 * time.Second)

	// Execute runtime changes
	for _, obj := range changedObjs {
		if deletedObj, ok := obj.(*queue.Deleted); ok {
			if err := c.Delete(ctx, deletedObj.Object); err != nil {
				t.Fatalf("Failed to delete object in fake client: %v", err)
			}
		} else {
			if err := c.Update(ctx, obj, client.FieldOwner(testingfake.FieldManager)); err != nil {
				t.Fatalf("Failed to update object in fake client: %v", err)
			}
		}
	}

	// Configure the Applier to start erroring on Update.
	// This will prevent the reconciler from reverting the ClusterRoleObject
	// change, and prevent the queue from emptying.
	a.UpdateError = status.APIServerError(fmt.Errorf("fake update error"), "updating")

	// Simulate watch events to add the objects to the queue
	for _, obj := range changedObjs {
		q.Add(obj)
	}

	// Let the worker run for a bit and then stop it.
	time.Sleep(1 * time.Second)
	cancel()

	// Wait for worker to exit or timeout
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	select {
	case <-timeout.C:
		// fail
		t.Error("Run() with empty queue did not return when context was cancelled")
	case <-doneCh:
		// pass
		c.Check(t, expectedObjs...)
	}
}

func TestWorker_Refresh(t *testing.T) {
	name := "admin"
	namespace := "shipping"
	scheme := runtime.NewScheme()
	err := v1.AddToScheme(scheme)
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name        string
		queue       fakeQueue
		client      client.Client
		want        *unstructured.Unstructured
		wantDeleted bool
		wantErr     status.Error
	}{
		{
			name: "Not found marks object deleted",
			queue: fakeQueue{
				element: k8sobjects.UnstructuredObject(kinds.Role(), core.Name(name), core.Namespace(namespace)),
			},
			client:      syncertestfake.NewClient(t, scheme),
			want:        k8sobjects.UnstructuredObject(kinds.Role(), core.Name(name), core.Namespace(namespace)),
			wantDeleted: true,
			wantErr:     nil,
		},
		{
			name: "Found updates objects",
			queue: fakeQueue{
				element: k8sobjects.UnstructuredObject(kinds.Role(), core.Name(name), core.Namespace(namespace),
					core.Annotation("foo", "bar")),
			},
			client: syncertestfake.NewClient(t, scheme,
				k8sobjects.RoleObject(core.Name(name), core.Namespace(namespace),
					core.Annotation("foo", "qux"))),
			want: k8sobjects.UnstructuredObject(kinds.Role(), core.Name(name), core.Namespace(namespace),
				core.UID("1"), core.ResourceVersion("1"), core.Generation(1),
				core.Annotation("foo", "qux")),
			wantDeleted: false,
			wantErr:     nil,
		},
		{
			name: "API Error does not update object",
			queue: fakeQueue{
				element: k8sobjects.UnstructuredObject(kinds.Role(), core.Name(name), core.Namespace(namespace)),
			},
			client:      syncertestfake.NewErrorClient(errors.New("some error")),
			want:        k8sobjects.UnstructuredObject(kinds.Role(), core.Name(name), core.Namespace(namespace)),
			wantDeleted: false,
			wantErr: status.APIServerError(errors.New("some error"),
				"failed to get updated object for remediator worker cache",
				k8sobjects.UnstructuredObject(kinds.Role(), core.Name(name), core.Namespace(namespace))),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			w := &Worker{
				objectQueue: &tc.queue,
				reconciler: fakeReconciler{
					client: tc.client,
				},
			}

			err := w.refresh(context.Background(), k8sobjects.UnstructuredObject(
				kinds.Role(), core.Name(name), core.Namespace(namespace)))
			testerrors.AssertEqual(t, tc.wantErr, err)

			if !tc.wantDeleted && tc.wantErr == nil {
				// These fields are added by unstructured conversions, but we aren't
				// testing this behavior.
				_ = unstructured.SetNestedField(tc.want.Object, nil, "rules")
				unstructured.RemoveNestedField(tc.want.Object, "metadata", "labels")
			}

			var want client.Object = tc.want
			if tc.wantDeleted {
				want = queue.MarkDeleted(context.Background(), want)
			}

			if diff := cmp.Diff(want, tc.queue.element); diff != "" {
				t.Error(diff)
			}
		})
	}
}

func randomCommitHash() string {
	return uuid.NewString()
}

type fakeReconciler struct {
	client       client.Client
	remediateErr status.Error
}

var _ reconcilerInterface = fakeReconciler{}

func (f fakeReconciler) Remediate(_ context.Context, _ core.ID, _ client.Object) status.Error {
	return f.remediateErr
}

func (f fakeReconciler) GetClient() client.Client {
	return f.client
}

type fakeQueue struct {
	queue.Interface
	element client.Object
}

func (q *fakeQueue) Add(o client.Object) {
	q.element = o
}

func (q *fakeQueue) Retry(o client.Object) {
	q.element = o
}

func (q *fakeQueue) Forget(_ client.Object) {
	q.element = nil
}
