package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"log"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sort"
	"strings"
)

const BINDING_FINALIZER = "binding.capabilities.3scale.net"

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// BindingSpec defines the desired state of Binding
type BindingSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	CredentialsRef v1.SecretReference `json:"credentialsRef"`
	//+optional
	APISelector metav1.LabelSelector `json:"APISelector,omitempty"`
}

// BindingStatus defines the observed state of Binding
type BindingStatus struct {
	//+optional
	LastSuccessfulSync *metav1.Timestamp `json:"lastSync,omitempty"`
	//+optional
	CurrentState *string `json:"currentState,omitempty"`
	//+optional
	DesiredState *string `json:"desiredState,omitempty"`
	//+optional
	PreviousState *string `json:"previousState,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Binding is the Schema for the bindings API
// +k8s:openapi-gen=true
type Binding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BindingSpec   `json:"spec,omitempty"`
	Status BindingStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// BindingList contains a list of Binding
type BindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Binding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Binding{}, &BindingList{})
}

type State struct {
	Credentials InternalCredentials `json:"credentials"`
	APIs        []InternalAPI       `json:"apis"`
}

func (s *State) Sort() {

	for _, api := range s.APIs {
		api.Sort()
	}

	sort.Slice(s.APIs, func(i, j int) bool {
		if s.APIs[i].Name != s.APIs[j].Name {
			return s.APIs[i].Name < s.APIs[j].Name
		} else {
			return s.APIs[i].Description < s.APIs[j].Description
		}
	})
}

func CompareStates(A, B State) bool {

	//Check the credentials
	if !reflect.DeepEqual(A.Credentials, B.Credentials) {
		return false
	}

	// Check if we have the same number of APIs
	if len(A.APIs) == len(B.APIs) {

		A.Sort()
		B.Sort()

		// Compare APIs one by one.
		for i := range A.APIs {
			if !CompareInternalAPI(A.APIs[i], B.APIs[i]) {
				return false
			}
		}
	} else {
		return false
	}
	return true
}

// Updates the Binding Object Status with the Desired and Current State
func (b *Binding) UpdateStatus(c client.Client) error {

	err := c.Status().Update(context.TODO(), b)
	if err != nil {
		return err
	}

	return nil
}
func (b *Binding) SetLastSuccessfulSync() {
	now := metav1.Now()
	timestamp := now.ProtoTime()
	b.Status.LastSuccessfulSync = timestamp
}
func (b *Binding) IsTerminating() bool {
	return b.HasFinalizer() && b.DeletionTimestamp != nil
}
func (b *Binding) CleanUp(c client.Client) error {

	state, err := b.GetCurrentState()
	portaClient, err := NewPortaClient(state.Credentials)

	for _, api := range state.APIs {
		api.DeleteFrom3scale(portaClient)
	}

	//Remove finalizer
	finalizers := b.GetFinalizers()
	var setFinalizers []string
	for _, finalizer := range finalizers {
		if finalizer != BINDING_FINALIZER {
			setFinalizers = append(setFinalizers, finalizer)
		}
	}
	b.SetFinalizers(setFinalizers)
	err = c.Update(context.TODO(), b)
	if err != nil {
		return err
	}
	return nil
}
func (b *Binding) AddFinalizer(c client.Client) error {
	finalizers := b.GetFinalizers()
	bindingFinalizer := BINDING_FINALIZER
	finalizers = append(finalizers, bindingFinalizer)
	b.SetFinalizers(finalizers)
	return c.Update(context.TODO(), b)

}
func (b *Binding) SetDesiredState(state State) error {
	byteState, err := json.Marshal(state)
	if err != nil {
		return err
	}
	desiredState := string(byteState)
	b.Status.DesiredState = &desiredState
	return nil
}
func (b *Binding) SetCurrentState(state State) error {
	byteState, err := json.Marshal(state)
	if err != nil {
		return err
	}
	currentState := string(byteState)
	b.Status.CurrentState = &currentState
	return nil
}
func (b *Binding) SetPreviousState(state State) error {
	byteState, err := json.Marshal(state)
	if err != nil {
		return err
	}
	previousState := string(byteState)
	b.Status.PreviousState = &previousState
	return nil
}
func (b *Binding) StateInSync() bool {

	if b.Status.CurrentState == nil || b.Status.DesiredState == nil {
		return false
	}

	var err error

	currentState, err := b.GetCurrentState()
	if err != nil {
		return false
	}

	desiredState, err := b.GetDesiredState()
	if err != nil {
		return false
	}

	return CompareStates(*desiredState, *currentState)
}
func (b Binding) GetLastSuccessfulSync() *metav1.Timestamp {
	if b.Status.LastSuccessfulSync != nil {
		return b.Status.LastSuccessfulSync
	}

	return nil
}
func (b Binding) HasFinalizer() bool {

	finalizers := b.Finalizers

	if len(finalizers) != 0 {
		for _, finalizer := range finalizers {
			if finalizer == BINDING_FINALIZER {
				return true
			}
		}
	}

	return false
}
func (b Binding) NewDesiredState(c client.Client) (*State, error) {

	internalCredentials, err := b.NewInternalCredentials(c)
	if err != nil {
		return nil, err
	}

	state := State{
		Credentials: *internalCredentials,
		APIs:        nil,
	}

	apis, err := b.GetAPIs(c)
	if err != nil && errors.IsNotFound(err) {
		// No API objects
		return nil, err
	} else if err != nil {
		// Something is broken
		return nil, err
	}

	// Add each API info to the consolidated object
	for _, api := range apis.Items {
		internalAPI, err := api.GetInternalAPI(c)
		if err != nil {
			log.Printf("Error on InternalAPI: %s", err)
		} else {
			state.APIs = append(state.APIs, *internalAPI)
		}
	}

	state.Sort()
	return &state, nil

}
func (b Binding) NewCurrentState(c client.Client) (*State, error) {

	internalCredentials, err := b.NewInternalCredentials(c)
	if err != nil {
		return nil, err
	}

	state := State{
		Credentials: *internalCredentials,
		APIs:        nil,
	}

	apis, err := b.GetAPIs(c)
	if err != nil && errors.IsNotFound(err) {
		// No API objects
		log.Printf("Binding: %s in namespace: %s doesn't match any API object", b.Name, b.Namespace)
		return nil, err
	} else if err != nil {
		// Something is broken
		return nil, err
	}

	portaClient, err := NewPortaClient(state.Credentials)
	if err != nil {
		return nil, err
	}

	for _, api := range apis.Items {
		internalAPI, err := api.getInternalAPIfrom3scale(portaClient)
		if err != nil && strings.Contains(err.Error(), "NotFound") {
			// Nothing has been found
			log.Printf("API is missing from 3scale: %s\n", api.Name)
		} else if err != nil {
			// Something is broken
			return nil, err
		} else {
			state.APIs = append(state.APIs, *internalAPI)
		}
	}

	state.Sort()
	return &state, nil
}
func (b Binding) GetAPIs(c client.Client) (*APIList, error) {
	apis := &APIList{}
	opts := &client.ListOptions{}
	opts.InNamespace(b.Namespace)
	opts.MatchingLabels(b.Spec.APISelector.MatchLabels)
	err := c.List(context.TODO(), opts, apis)
	return apis, err
}
func (b Binding) NewInternalCredentials(c client.Client) (*InternalCredentials, error) {

	// GET SECRET
	secret := &v1.Secret{}
	// TODO: fix namespace default
	err := c.Get(context.TODO(), types.NamespacedName{Name: b.Spec.CredentialsRef.Name, Namespace: b.Namespace}, secret)

	if err != nil && errors.IsNotFound(err) {
		return nil, fmt.Errorf("credentialsNotFound")
	} else if err != nil {
		return nil, fmt.Errorf("errorGettingCredentials")
	}

	return &InternalCredentials{
		AuthToken: string(secret.Data["token"]),
		AdminURL:  string(secret.Data["adminURL"]),
	}, nil
}
func (b Binding) GetPreviousState() (*State, error) {
	if b.Status.PreviousState != nil {

		previousState := State{}
		err := json.Unmarshal([]byte(*b.Status.PreviousState), &previousState)
		if err != nil {
			return nil, err
		}
		return &previousState, nil
	}
	return nil, nil
}
func (b Binding) GetDesiredState() (*State, error) {
	if b.Status.DesiredState != nil {
		currentState := State{}

		err := json.Unmarshal([]byte(*b.Status.DesiredState), &currentState)
		if err != nil {
			return nil, err
		}

		return &currentState, nil
	}
	return nil, nil
}
func (b Binding) GetCurrentState() (*State, error) {
	if b.Status.CurrentState != nil {

		desiredState := State{}

		err := json.Unmarshal([]byte(*b.Status.CurrentState), &desiredState)
		if err != nil {
			return nil, err
		}
		return &desiredState, nil
	}
	return nil, nil
}
