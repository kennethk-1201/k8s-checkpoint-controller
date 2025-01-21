/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PodMigrationSpec defines the desired state of PodMigration
type PodMigrationSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:MinLength=0
	PodName string `json:"podName"`

	// +kubebuilder:validation:MinLength=0
	NewPodName string `json:"newPodName"`

	// +kubebuilder:validation:MinLength=0
	// +optional
	NodeName string `json:"nodeName"`

	// +optional
	NodeSelector map[string]string `json:"nodeSelector"`
}

type PodMigrationPhase string

const (
	Pending   PodMigrationPhase = "Pending"
	Restoring PodMigrationPhase = "Restoring"
	Succeeded PodMigrationPhase = "Succeeded"
	Failed    PodMigrationPhase = "Failed"
	Unknown   PodMigrationPhase = "Unknown"
)

// PodMigrationStatus defines the observed state of PodMigration
type PodMigrationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:default:="Pending"
	// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=`.status.Phase`
	Phase PodMigrationPhase `json:"status"`

	// Information when was the migration completed
	LastCompletedTime *metav1.Time `json:"lastScheduleTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="STATUS",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// PodMigration is the Schema for the podmigrations API
type PodMigration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PodMigrationSpec `json:"spec,omitempty"`

	// +kubebuilder:default:={"status":"Pending"}
	Status PodMigrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PodMigrationList contains a list of PodMigration
type PodMigrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PodMigration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PodMigration{}, &PodMigrationList{})
}
