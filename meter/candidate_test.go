// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package meter_test

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/meterio/meter-pov/meter"
)

/*
Execute this test with
cd /tmp/meter-build-xxxxx/src/github.com/meterio/meter-pov/script/staking
GOPATH=/tmp/meter-build-xxxx/:$GOPATH go test
*/

const TestAddress1 = "0x1de8ca2f973d026300af89041b0ecb1c0803a7e6"
const TestAddress2 = "0x8e69e4357d886b8dd3131af7d7627a4381d3ddd4"
const TestAddress3 = "0x0205c2D862cA051010698b69b54278cbAf945C0b"
const TestAddress4 = "0x8A88c59bF15451F9Deb1d62f7734FeCe2002668E"

func permute(path [][]int, candidates []int) [][]int {
	if len(candidates) == 0 {
		return path
	}
	result := make([][]int, 0)
	for i, c := range candidates {
		extendedPath := make([][]int, 0)
		newCandidates := make([]int, i)
		copy(newCandidates, candidates[:i])
		newCandidates = append(newCandidates, candidates[i+1:]...)
		if len(path) == 0 {
			extendedPath = append(extendedPath, []int{c})
		} else {
			for _, p := range path {
				extendedPath = append(extendedPath, append(p, c))
			}
		}
		newPaths := permute(extendedPath, newCandidates)
		for _, p := range newPaths {
			result = append(result, p)
		}
	}
	return result
}

func getCandidateList() (*meter.CandidateList, []*meter.Candidate) {
	cl := meter.NewCandidateList([]*meter.Candidate{})

	addr1, _ := meter.ParseAddress(TestAddress1)
	addr2, _ := meter.ParseAddress(TestAddress2)
	addr3, _ := meter.ParseAddress(TestAddress3)
	addr4, _ := meter.ParseAddress(TestAddress3)
	t := uint64(time.Now().Unix())
	c1 := meter.NewCandidate(addr1, []byte("candidate #1"), []byte("pubkey #1"), []byte("ip1"), []byte("8080"), 0, 0, t)
	c2 := meter.NewCandidate(addr2, []byte("candidate #2"), []byte("pubkey #2"), []byte("ip2"), []byte("8080"), 0, 0, t)
	c3 := meter.NewCandidate(addr3, []byte("candidate #3"), []byte("pubkey #3"), []byte("ip3"), []byte("8080"), 0, 0, t)
	c4 := meter.NewCandidate(addr4, []byte("candidate #4"), []byte("pubkey #4"), []byte("ip4"), []byte("8080"), 0, 0, t)
	cs := []*meter.Candidate{c1, c2, c3, c4}

	cl.Add(c1)
	cl.Add(c2)
	cl.Add(c3)
	cl.Add(c4)
	return cl, cs
}
func TestAddWithAllPossibleOrder(t *testing.T) {
	cl, cs := getCandidateList()
	buf := bytes.NewBuffer([]byte{})
	e := gob.NewEncoder(buf)
	e.Encode(cl)
	expected := buf.Bytes()

	indexes := make([]int, len(cs))
	for i := 0; i < len(cs); i++ {
		indexes[i] = i
	}
	permutations := permute(make([][]int, 0), indexes)
	for _, perm := range permutations {
		cl = meter.NewCandidateList([]*meter.Candidate{})
		buf.Reset()
		for _, i := range perm {
			cl.Add(cs[i])
		}
		e = gob.NewEncoder(buf)
		e.Encode(cl)
		actual := buf.Bytes()
		if bytes.Compare(expected, actual) != 0 {
			fmt.Println("Error index order: ", perm)
			expectedHex := hex.EncodeToString(expected)
			actualHex := hex.EncodeToString(actual)
			fmt.Println("Expected Hex: ", expectedHex, ", But got: ", actualHex)
			t.Fail()
		}

	}
}

func TestRemove(t *testing.T) {
	_, cs := getCandidateList()
	indexes := make([]int, len(cs))
	for i := 0; i < len(cs); i++ {
		indexes[i] = i
	}
	permutations := permute(make([][]int, 0), indexes)
	for _, perm := range permutations {
		cl, _ := getCandidateList()
		for _, i := range perm {
			cl.Remove(cs[i].Addr)
			if cl.Exist(cs[i].Addr) {
				t.Fail()
			}
		}
		if cl.Count() != 0 {
			t.Fail()
		}
	}
}

func TestPermute(t *testing.T) {

	fmt.Println("_____________________PERMUTE______________________")
	permutation := permute(make([][]int, 0), []int{0, 1, 2, 3})
	// for _, p := range permutation {
	// fmt.Println("permutation: ", p)
	// }
	if len(permutation) != 24 {
		t.Fail()
	}
}
