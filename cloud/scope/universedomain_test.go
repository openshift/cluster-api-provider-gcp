/*
Copyright 2026 The Kubernetes Authors.

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

package scope

import "testing"

func TestIsNonDefaultUniverseDomain(t *testing.T) {
	tests := []struct {
		name           string
		universeDomain string
		want           bool
	}{
		{
			name:           "default googleapis.com",
			universeDomain: "googleapis.com",
			want:           false,
		},
		{
			name:           "empty string",
			universeDomain: "",
			want:           false,
		},
		{
			name:           "sovereign cloud domain",
			universeDomain: "googleapis.example.com",
			want:           true,
		},
		{
			name:           "custom universe domain",
			universeDomain: "custom.universe.domain",
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNonDefaultUniverseDomain(tt.universeDomain)
			if got != tt.want {
				t.Errorf("IsNonDefaultUniverseDomain(%q) = %v, want %v", tt.universeDomain, got, tt.want)
			}
		})
	}
}
