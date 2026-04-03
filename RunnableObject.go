// Copyright 2025 IBM Corp
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

package main

import (
	"fmt"
	"os"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go

const (
	// defaultRunnableObjectCapacity is the default capacity for runnable object slices
	defaultRunnableObjectCapacity = 5
)

// RunnableObject defines the interface for objects that can be executed in the cluster lifecycle.
// Implementations must provide methods for identification, execution, status checking, and prioritization.
//
// The interface is used to manage various cluster components (DNS, load balancers, VMs, etc.)
// in a consistent and prioritized manner.
type RunnableObject interface {
	// Name returns the display name of the runnable object.
	// This is typically used for user-facing output and logging.
	Name() (string, error)

	// ObjectName returns the internal object name used for identification.
	// This should be unique and used for internal tracking and logging.
	ObjectName() (string, error)

	// Run executes the main operation of the runnable object.
	// This is called during cluster initialization or setup.
	Run() error

	// ClusterStatus checks and reports the status of the object in the cluster.
	// This is typically called to validate cluster health and configuration.
	ClusterStatus()

	// Priority returns the execution priority of the object.
	// Higher priority objects are executed first. A priority of -1 indicates
	// no specific ordering requirement.
	Priority() (int, error)
}

// NewRunnableObject is a constructor function type that creates a single RunnableObject.
//
// Parameters:
//   - *Services: Services instance containing configuration and API clients
//
// Returns:
//   - RunnableObject: The created runnable object
//   - error: Any error encountered during creation
type NewRunnableObject func(*Services) (RunnableObject, error)

// NewRunnableObjects is a constructor function type that creates multiple RunnableObjects.
//
// Parameters:
//   - *Services: Services instance containing configuration and API clients
//
// Returns:
//   - []RunnableObject: Array of created runnable objects
//   - []error: Array of errors encountered during creation (one per object)
type NewRunnableObjects func(*Services) ([]RunnableObject, []error)

// NewRunnableObjectEntry wraps a single-object constructor with a descriptive name.
// This is used for registration and identification of object constructors.
type NewRunnableObjectEntry struct {
	// NRO is the constructor function for creating a single runnable object
	NRO NewRunnableObject
	// Name is the display name for this type of runnable object
	Name string
}

// NewRunnableObjectsEntry wraps a multi-object constructor with a descriptive name.
// This is used for registration and identification of object constructors that
// may create multiple instances (e.g., multiple VMs or load balancers).
type NewRunnableObjectsEntry struct {
	// NRO is the constructor function for creating multiple runnable objects
	NRO NewRunnableObjects
	// Name is the display name for this type of runnable objects
	Name string
}

// getPriority safely extracts the priority from a RunnableObject.
// Returns -1 if the priority cannot be determined or an error occurs.
//
// Parameters:
//   - ro: The runnable object to get priority from
//
// Returns:
//   - int: The priority value, or -1 if unavailable
func getPriority(ro RunnableObject) int {
	if ro == nil {
		log.Debugf("getPriority: nil RunnableObject, returning -1")
		return -1
	}

	priority, err := ro.Priority()
	if err != nil {
		name, nameErr := ro.ObjectName()
		if nameErr != nil {
			name = "unknown"
		}
		log.Debugf("getPriority: Failed to get priority for %s: %v, returning -1", name, err)
		return -1
	}

	return priority
}

// BubbleSort sorts an array of RunnableObjects by priority in descending order.
// Objects with higher priority values are placed first in the array.
// Objects with priority -1 or errors are placed at the end.
//
// This function uses bubble sort algorithm which is simple and works well for
// small arrays (typically < 50 elements). For larger arrays, consider using
// a more efficient sorting algorithm.
//
// Parameters:
//   - input: Array of RunnableObjects to sort
//
// Returns:
//   - []RunnableObject: Sorted array (same array, modified in place)
//
// Example:
//   objects := []RunnableObject{obj1, obj2, obj3}
//   sorted := BubbleSort(objects)
//   // sorted[0] has the highest priority
func BubbleSort(input []RunnableObject) []RunnableObject {
	if len(input) <= 1 {
		return input
	}

	log.Debugf("BubbleSort: Sorting %d runnable objects by priority", len(input))

	swapped := true
	// While we have swapped at least one element...
	for swapped {
		swapped = false
		for i := 1; i < len(input); i++ {
			// Does the next element have a higher priority than this element?
			if getPriority(input[i]) > getPriority(input[i-1]) {
				// Then swap them!
				input[i], input[i-1] = input[i-1], input[i]
				swapped = true
			}
		}
	}

	// Log the sorted order for debugging
	if log.Level >= 5 { // Debug level
		for i, obj := range input {
			name, _ := obj.ObjectName()
			priority := getPriority(obj)
			log.Debugf("BubbleSort: Position %d: %s (priority: %d)", i, name, priority)
		}
	}

	return input
}

// initializeRunnableObjects creates and initializes all runnable objects for cluster operations.
// It performs the following steps:
//  1. Calls constructor functions to create runnable objects
//  2. Validates each object (name and priority)
//  3. Collects all objects into a single array
//  4. Executes the Run() method on each object
//
// The function reports progress to stderr and logs detailed information for debugging.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//   - robjsFuncs: Array of constructor function entries to create runnable objects
//
// Returns:
//   - []RunnableObject: Array of successfully initialized runnable objects
//   - error: Any error encountered during initialization or execution
//
// Example:
//   objects, err := initializeRunnableObjects(services, []NewRunnableObjectsEntry{
//       {NRO: NewIBMDNS, Name: "IBM DNS"},
//       {NRO: NewLoadBalancer, Name: "Load Balancer"},
//   })
func initializeRunnableObjects(services *Services, robjsFuncs []NewRunnableObjectsEntry) ([]RunnableObject, error) {
	if services == nil {
		return nil, fmt.Errorf("services cannot be nil")
	}
	if len(robjsFuncs) == 0 {
		log.Debugf("initializeRunnableObjects: No runnable object functions provided")
		return []RunnableObject{}, nil
	}

	log.Debugf("initializeRunnableObjects: Initializing %d runnable object types", len(robjsFuncs))

	robjsCluster := make([]RunnableObject, 0, defaultRunnableObjectCapacity)

	// Loop through New functions which return an array of runnable objects.
	for i, nroe := range robjsFuncs {
		if nroe.NRO == nil {
			log.Debugf("initializeRunnableObjects: Skipping entry %d with nil constructor", i)
			continue
		}
		if nroe.Name == "" {
			log.Debugf("initializeRunnableObjects: Entry %d has empty name, using 'Unknown'", i)
			nroe.Name = "Unknown"
		}

		fmt.Fprintf(os.Stderr, "Querying the %s...\n", nroe.Name)
		log.Debugf("initializeRunnableObjects: Creating %s objects", nroe.Name)

		// Call the New function.
		robjsResult, errs := nroe.NRO(services)

		// Check for errors in object creation
		hasErrors := false
		for j, err := range errs {
			if err != nil {
				hasErrors = true
				fmt.Fprintf(os.Stderr, "Error: Could not create a %s object (index %d): %v\n", nroe.Name, j, err)
				log.Debugf("initializeRunnableObjects: Error creating %s object %d: %v", nroe.Name, j, err)
			}
		}

		if hasErrors {
			log.Debugf("initializeRunnableObjects: %s had creation errors, but continuing with valid objects", nroe.Name)
		}

		// Process successfully created objects
		for j, robj := range robjsResult {
			if robj == nil {
				log.Debugf("initializeRunnableObjects: Skipping nil object at index %d for %s", j, nroe.Name)
				continue
			}

			// Get the runnable object's name
			robjObjectName, err := robj.ObjectName()
			if err != nil {
				return nil, fmt.Errorf("failed to get object name for %s (index %d): %w", nroe.Name, j, err)
			}

			// Validate the priority
			priority, err := robj.Priority()
			if err != nil {
				return nil, fmt.Errorf("failed to get priority for %s: %w", robjObjectName, err)
			}

			// Append the runnable object
			log.Debugf("initializeRunnableObjects: Appending %s (priority: %d)", robjObjectName, priority)
			robjsCluster = append(robjsCluster, robj)
		}
	}

	log.Debugf("initializeRunnableObjects: Created %d runnable objects total", len(robjsCluster))

	// Run each object
	for i, robj := range robjsCluster {
		robjObjectName, err := robj.ObjectName()
		if err != nil {
			robjObjectName = fmt.Sprintf("unknown-object-%d", i)
			log.Debugf("initializeRunnableObjects: Could not get name for object %d: %v", i, err)
		}

		fmt.Fprintf(os.Stderr, "Running the %s...\n", robjObjectName)
		log.Debugf("initializeRunnableObjects: Running %s (object %d of %d)", robjObjectName, i+1, len(robjsCluster))

		if err := robj.Run(); err != nil {
			log.Debugf("initializeRunnableObjects: Failed to run %s: %v", robjObjectName, err)
			return nil, fmt.Errorf("failed to run %s: %w", robjObjectName, err)
		}

		log.Debugf("initializeRunnableObjects: Successfully ran %s", robjObjectName)
	}

	log.Debugf("initializeRunnableObjects: All %d objects initialized and run successfully", len(robjsCluster))
	return robjsCluster, nil
}
