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

package designate

import "testing"

func TestRndciKeyGenerator(t *testing.T) {
	result, err := CreateRndcKeySecret()
	if err != nil {
		t.Errorf("Error creating rndc key secret: %v", err)
	}

	if len(result) == 0 {
		t.Errorf("Expected a non-empty result from CreateRndcKeySecret")
	}

	result2, err := CreateRndcKeySecret()
	if result == result2 {
		t.Errorf("Expected different results from CreateRndcKeySecret calls")
	}

	if err != nil {
		t.Errorf("Error creating rndc key secret: %v", err)
	}

	if len(result2) == 0 {
		t.Errorf("Expected a non-empty result from CreateRndcKeySecret")
	}
	// I also tested manually the key by placing it on rndc.key and reset
	// designate services, bind9, and ran `rndc reload` successfully
}
