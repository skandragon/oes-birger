package agent

/*
 * Copyright 2021 OpsMx, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License")
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import "fmt"

// Endpoint defines the configuration and description provided by the
// agent.  This describes a service endpoint of a specific type.
// The tuple (Type, Name) must be unique per agent connection,
// although multiple agents (even with the same agent name) may
// provide the same endpoint.
type Endpoint struct {
	Name       string   `json:"name,omitempty"`
	Type       string   `json:"type,omitempty"`
	Configured bool     `json:"configured,omitempty"`
	Namespaces []string `json:"namespaces,omitempty"`
}

func (e *Endpoint) String() string {
	return fmt.Sprintf("(%s, %s, %v)", e.Type, e.Name, e.Configured)
}
