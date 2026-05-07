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

//
// Make sure the IPI installer can run.
//
// Note: The version sub-command genuinely has no use for the directory,
// and the param is kept only for signature uniformity.
//
func createClusterPhase1(_ string) error {
	return runSplitCommand([]string{
		"openshift-install",
		"version",
	})
}
