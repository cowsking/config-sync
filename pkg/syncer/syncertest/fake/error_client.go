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

package fake

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ErrorClient is a Client that always returns a specified error.
type ErrorClient struct {
	error error
}

// NewErrorClient returns a Client that always returns an error.
func NewErrorClient(err error) client.Client {
	return &ErrorClient{error: err}
}

// GroupVersionKindFor returns the GroupVersionKind for the given object.
func (e ErrorClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, fmt.Errorf("GroupVersionKindFor not implemented")
}

// IsObjectNamespaced returns true if the GroupVersionKind of the object is namespaced.
func (e ErrorClient) IsObjectNamespaced(_ runtime.Object) (bool, error) {
	return false, fmt.Errorf("IsObjectNamespaced not implemented")
}

// Get implements client.Client.
func (e ErrorClient) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return e.error
}

// List implements client.Client.
func (e ErrorClient) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return e.error
}

// Create implements client.Client.
func (e ErrorClient) Create(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
	return e.error
}

// Delete implements client.Client.
func (e ErrorClient) Delete(_ context.Context, _ client.Object, _ ...client.DeleteOption) error {
	return e.error
}

// Update implements client.Client.
func (e ErrorClient) Update(_ context.Context, _ client.Object, _ ...client.UpdateOption) error {
	return e.error
}

// Apply implements client.Client.
func (e ErrorClient) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...client.ApplyOption) error {
	return e.error
}

// Patch implements client.Client.
func (e ErrorClient) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
	return e.error
}

// DeleteAllOf implements client.Client.
func (e ErrorClient) DeleteAllOf(_ context.Context, _ client.Object, _ ...client.DeleteAllOfOption) error {
	return e.error
}

// SubResource implements client.Client.
func (e ErrorClient) SubResource(_ string) client.SubResourceClient {
	return SubResourceErrorClient(e)
}

// Status implements client.Client.
func (e ErrorClient) Status() client.StatusWriter {
	return SubResourceErrorClient(e)
}

// Scheme implements client.Client.
func (e ErrorClient) Scheme() *runtime.Scheme {
	panic("fake.ErrorClient does not support Scheme()")
}

// RESTMapper implements client.Client.
func (e ErrorClient) RESTMapper() meta.RESTMapper {
	panic("fake.ErrorClient does not support RESTMapper()")
}

// SubResourceErrorClient is a SubResourceClient that always returns a specified error.
type SubResourceErrorClient struct {
	error error
}

var _ client.SubResourceClient = SubResourceErrorClient{}

// Get implements client.SubResourceReader
func (e SubResourceErrorClient) Get(_ context.Context, _ client.Object, _ client.Object, _ ...client.SubResourceGetOption) error {
	return e.error
}

// Create implements client.SubResourceWriter
func (e SubResourceErrorClient) Create(_ context.Context, _ client.Object, _ client.Object, _ ...client.SubResourceCreateOption) error {
	return e.error
}

// Update implements client.SubResourceWriter
func (e SubResourceErrorClient) Update(_ context.Context, _ client.Object, _ ...client.SubResourceUpdateOption) error {
	return e.error
}

// Patch implements client.SubResourceWriter
func (e SubResourceErrorClient) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
	return e.error
}
