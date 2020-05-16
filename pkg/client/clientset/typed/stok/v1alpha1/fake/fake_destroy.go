// Copyright © 2020 Louis Garman <louisgarman@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1alpha1 "github.com/leg100/stok/pkg/apis/stok/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeDestroys implements DestroyInterface
type FakeDestroys struct {
	Fake *FakeStokV1alpha1
	ns   string
}

var destroysResource = schema.GroupVersionResource{Group: "stok.goalspike.com", Version: "v1alpha1", Resource: "destroys"}

var destroysKind = schema.GroupVersionKind{Group: "stok.goalspike.com", Version: "v1alpha1", Kind: "Destroy"}

// Get takes name of the destroy, and returns the corresponding destroy object, and an error if there is any.
func (c *FakeDestroys) Get(name string, options v1.GetOptions) (result *v1alpha1.Destroy, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(destroysResource, c.ns, name), &v1alpha1.Destroy{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Destroy), err
}

// List takes label and field selectors, and returns the list of Destroys that match those selectors.
func (c *FakeDestroys) List(opts v1.ListOptions) (result *v1alpha1.DestroyList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(destroysResource, destroysKind, c.ns, opts), &v1alpha1.DestroyList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.DestroyList{ListMeta: obj.(*v1alpha1.DestroyList).ListMeta}
	for _, item := range obj.(*v1alpha1.DestroyList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested destroys.
func (c *FakeDestroys) Watch(opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchAction(destroysResource, c.ns, opts))

}

// Create takes the representation of a destroy and creates it.  Returns the server's representation of the destroy, and an error, if there is any.
func (c *FakeDestroys) Create(destroy *v1alpha1.Destroy) (result *v1alpha1.Destroy, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewCreateAction(destroysResource, c.ns, destroy), &v1alpha1.Destroy{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Destroy), err
}

// Update takes the representation of a destroy and updates it. Returns the server's representation of the destroy, and an error, if there is any.
func (c *FakeDestroys) Update(destroy *v1alpha1.Destroy) (result *v1alpha1.Destroy, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateAction(destroysResource, c.ns, destroy), &v1alpha1.Destroy{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Destroy), err
}

// Delete takes name of the destroy and deletes it. Returns an error if one occurs.
func (c *FakeDestroys) Delete(name string, options *v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteAction(destroysResource, c.ns, name), &v1alpha1.Destroy{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeDestroys) DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error {
	action := testing.NewDeleteCollectionAction(destroysResource, c.ns, listOptions)

	_, err := c.Fake.Invokes(action, &v1alpha1.DestroyList{})
	return err
}

// Patch applies the patch and returns the patched destroy.
func (c *FakeDestroys) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1alpha1.Destroy, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceAction(destroysResource, c.ns, name, pt, data, subresources...), &v1alpha1.Destroy{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Destroy), err
}