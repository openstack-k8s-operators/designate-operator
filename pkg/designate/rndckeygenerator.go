/*

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

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log"
	"math/big"
)

const (
	MinPasswordSize = 25
)

// Generate a random string of specified length
func genword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyz" +
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	password := make([]byte, length)
	for i := range password {
		randomIndex, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		password[i] = charset[randomIndex.Int64()]
	}
	return string(password), nil
}

// Create the rndc key secret
func CreateRndcKeySecret() (string, error) {
	// Generate random strings
	key, err := genword(MinPasswordSize)
	if err != nil {
		log.Fatalf("Error creating rndc hmac key: %v", err)
		return "", err
	}

	msg, err := genword(MinPasswordSize)
	if err != nil {
		log.Fatalf("Error creating rndc hmac message: %v", err)
		return "", err
	}

	// Create HMAC-SHA256 digest
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(msg))
	digest := h.Sum(nil)

	// Encode to base64
	secret := base64.StdEncoding.EncodeToString(digest)
	return secret, nil
}
