package main

import (
	"log"
	"math/rand"
	"net/http"
)

/*
 * Copyright 2023 OpsMx, Inc.
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

func check(err error) {
	if err != nil {
		log.Fatalf("%v", err)
	}
}

var letters = []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randomData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return b
}

func traffic(w http.ResponseWriter, r *http.Request) {
	s := randomData(100000)
	for i := 0; i < 100; i++ {
		i, err := w.Write(s)
		if err != nil {
			log.Printf("Write error: %v", err)
			return
		}
		if i != len(s) {
			log.Printf("Short write: expected %d, wrote %d", len(s), i)
			return
		}
	}
}

func main() {
	http.HandleFunc("/traffic", traffic)

	log.Printf("Starting HTTP server on port 8100")
	log.Fatalf("%v", http.ListenAndServe(":8100", nil))
}
