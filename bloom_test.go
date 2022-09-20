// Copyright 2021 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zoekt // import "github.com/sourcegraph/zoekt"

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"math/rand"
	"os"
	"path"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var (
	ngramDataDir = flag.String("ngramdir", "", "directory containing testdata with files with one word per line")
	docCount     = flag.Int("docs", 0, "number of docs to load, (default 0 for all)")
	hasherNum    = flag.Int("hasher", 1, "index of the hasher to test")
	loadPerc     = flag.String("load", "42", "space-separated lists of target load percentages")
	docFpr       = flag.Bool("docfpr", false, "show per-document FPRs")
	trigramFpr   = flag.Bool("tri", false, "compute FPR for trigram-based filtering")
)

func TestFindNextWord(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
	}{
		{
			"aeiou and SOMETIMES y",
			[]string{"aeiou", "sometimes"},
		},
		{
			"\n//_azAZ09[~]3456",
			[]string{"_azaz09", "3456"},
		},
		{
			"nee\u212A aa\u212A", // kelvin degree symbol => 'k'
			[]string{"neek"},
		},
	} {
		out := []string{}
		in := []byte(tc.input)
		for i := 0; i < len(in); {
			var s []byte
			i, s = findNextWord(i, in)
			if s != nil {
				out = append(out, string(s))
			}
		}
		if !reflect.DeepEqual(tc.want, out) {
			t.Errorf("findNextWord(%q) got %q want %q", tc.input, out, tc.want)
		}
	}
}

func TestBloomHasher(t *testing.T) {
	b := makeBloomFilterEmpty()
	hashCount := len(b.hasher([]byte("testing")))
	expected := 3 * len(strings.Split("test testi testin testing esti estin esting stin sting ting", " "))
	if hashCount != expected {
		t.Errorf("hasher(\"testing\") produced %d hashes instead of %d", hashCount, expected)
	}

	inpA := []byte("some inputs to the bloom filter hashing")
	inpB := []byte("SOME inputs to the bloom filter hashing a b cd")
	if !reflect.DeepEqual(b.hasher(inpA), b.hasher(inpB)) {
		t.Errorf("hash(%v) => %v != hash(%v) => %v", inpA, b.hasher(inpA), inpB, b.hasher(inpB))
	}
}

func TestBloomHasherStability(t *testing.T) {
	want := []uint32{
		0x41b0c462, 0x41b0c46c, 0x41b0c5a8, 0x79882c16, 0x79882c62, 0x79882d0f,
		0x79882dbc, 0x79882d03, 0x79882d64, 0x79882cfd, 0x79882d90, 0x79882c74,
		0x79882d79, 0x79882d75, 0x79882df3, 0xde692090, 0xde69219a, 0xde6920db,
		0xde6920c0, 0xde6921ce, 0xde692132, 0xde6920a7, 0xde69207b, 0xde69201a,
		0xde6920df, 0xde69214b, 0xde692183, 0x814351a8, 0x81435050, 0x81435090,
		0x81435037, 0x814350db, 0x814350ce, 0x81435188, 0x8143509d, 0x81435113,
		0x814351bc, 0x814351b6, 0x81435054, 0x88772190, 0x8877201d, 0x887720b1,
		0x88772148, 0x8877208b, 0x887720b5, 0x88772154, 0x88772069, 0x887720aa,
		0x8877215c, 0x8877213a, 0x887720b2, 0x3654361b, 0x36543795, 0x365436c6,
		0x3654364e, 0x3654361a, 0x36543623, 0x365436ec, 0x3654365f, 0x3654364d,
		0x3654368b, 0x365437a4, 0x3654375c, 0x2d64f078, 0x2d64f159, 0x2d64f105,
		0x2d64f033, 0x2d64f145, 0x2d64f1ea, 0x2d64f130, 0x2d64f085, 0x2d64f029,
		0x2d64f0ad, 0x2d64f188, 0x2d64f148, 0xc9ba3319, 0xc9ba326e, 0xc9ba32d9,
		0xc9ba3381, 0xc9ba3331, 0xc9ba32ff, 0xc9ba320f, 0xc9ba335d, 0xc9ba3345,
		0xc9ba338a, 0xc9ba32aa, 0xc9ba3273, 0xc9cb6fb7, 0xc9cb6e72, 0xc9cb6fd9,
		0xc9cb6ed0, 0xc9cb6e47, 0xc9cb6ee2, 0xc9cb6e31, 0xc9cb6f8b, 0xc9cb6f06,
		0x07b383c1, 0x07b383ec, 0x07b38200, 0x07b3830a, 0x07b382ec, 0x07b3838d,
		0x90a95aad, 0x90a95a2a, 0x90a95bf2}
	have := bloomHasherCRCBlocked64B8K3([]byte("nee\u212A  STAbilizAtion??"))
	if !reflect.DeepEqual(have, want) {
		t.Error("Bloom hasher outputs have changed. This will break queries! Revert and add a new method.")
		t.Errorf("have=%#v want=%#v", have, want)
	}
}

func TestBloomZero(t *testing.T) {
	var b bloom
	if !b.maybeHasBytes([]byte("some example strings")) {
		t.Error("bloom{}.maybeHasBytes should always return true")
	}
	if !b.maybeHas([]uint32{123}) {
		t.Error("bloom{}.maybeHas should always return true")
	}
}

func TestBloomBasic(t *testing.T) {
	b := makeBloomFilterEmpty()

	// Edge case: empty bloom filter resizing
	b1 := b.shrinkToSize(0.9999)
	if b1.Len() != 8 {
		t.Error("Empty bloom filter didn't resize to 1B")
	}

	// Edge case: nearly empty bloom filter resizing
	b.addBytes([]byte("some"))
	b2 := b.shrinkToSize(0.999)
	if b2.Len() != 8 {
		t.Error("Nearly empty bloom filter didn't resize to 1B")
	}

	// these test strings are carefully selected to not collide
	// with the default hash functions.
	inp := []byte(`some different test words that will definitely be present
	within the bloom filter`)
	missed := []byte("somehow another sequences falsified probabilisitically")

	b.addBytes(inp)

	for i := 0; i < 90; i += 5 {
		bi := b.shrinkToSize(float64(i) * .01)
		t.Logf("target %d%% load: shrink %d=>%d bytes, load factor %.07f%% => %.02f%%",
			i, len(b.bits), len(bi.bits), b.load()*100, bi.load()*100)

		for _, w := range bytes.Split(inp, []byte{' '}) {
			if !bi.maybeHasBytes(w) {
				t.Errorf("%d filter should contain %q but doesn't", i, string(w))
			}
		}

		for _, w := range bytes.Split(missed, []byte{' '}) {
			if bi.maybeHasBytes(w) {
				t.Errorf("%d filter shouldn't contain %q but does", i, string(w))
			}
		}
	}
}

func BenchmarkBloomFilterResize(b *testing.B) {
	f := makeBloomFilterEmpty()

	rng := rand.New(rand.NewSource(123))
	for i := 0; i < 1e6; i++ {
		f.addBytes(randWord(4, 10, rng))
	}

	b.SetBytes(int64(len(f.bits)))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.shrinkToSize(bloomDefaultLoad)
	}
}

func randWord(min, max int, rng *rand.Rand) []byte {
	length := rng.Intn(max-min) + max
	out := make([]byte, length)
	for i := 0; i < length; i++ {
		out[i] = "abcdefghijklmnopqrstuvwxyz0123456789"[rng.Intn(36)]
	}
	return out
}

type customSort struct {
	len  func() int
	less func(i, j int) bool
	swap func(i, j int)
}

func (c *customSort) Len() int           { return c.len() }
func (c *customSort) Less(i, j int) bool { return c.less(i, j) }
func (c *customSort) Swap(i, j int)      { c.swap(i, j) }

func TestBloomFalsePositiveRate(t *testing.T) {
	rng := rand.New(rand.NewSource(123))

	var wg sync.WaitGroup
	var lock sync.Mutex
	cpuCount := runtime.NumCPU()

	var hasher bloomHash
	var hname string

	switch *hasherNum {
	case 0:
		hasher = bloomHasherCRC
		hname = "crc"
	case 1:
		hasher = bloomHasherCRCBlocked64B8K3
		hname = "crcblock64k3_8"
	}
	t.Log("hasher:", hname)

	targetRate := []int{}
	for _, n := range strings.Split(*loadPerc, " ") {
		tr, err := strconv.Atoi(n)
		if err != nil {
			t.Fatal(err)
		}
		targetRate = append(targetRate, tr)
	}
	t.Log("load percentage targets:", targetRate)
	totsize := make([]int, len(targetRate))
	docsize := 0

	docs := [][]byte{}
	docNames := []string{}
	blooms := [][]bloom{}

	addDoc := func(name string, doc []byte, parallel bool) ([]bloom, int) {
		b := makeBloomFilterWithHasher(hasher)
		b.addBytes(doc)
		bs := []bloom{}
		for _, r := range targetRate {
			bs = append(bs, b.shrinkToSize(float64(r)*0.01))
		}

		if parallel {
			lock.Lock()
		}
		docNames = append(docNames, name)
		blooms = append(blooms, bs)
		i := len(blooms)
		docs = append(docs, doc)
		docsize += len(doc)
		for i, b := range bs {
			totsize[i] += b.Len()
		}
		if parallel {
			lock.Unlock()
		}
		return bs, i
	}

	if *ngramDataDir != "" {
		dirents, err := os.ReadDir(*ngramDataDir)
		if err != nil {
			t.Fatal(err)
		}
		sort.Slice(dirents, func(i, j int) bool {
			return dirents[i].Name() < dirents[j].Name()
		})

		if *docCount > 0 && *docCount < len(dirents) {
			dirents = dirents[:*docCount]
		}

		work := make(chan fs.DirEntry)

		for i := 0; i < cpuCount; i++ {
			go func() {
				for dirent := range work {
					doc, err := os.ReadFile(path.Join(*ngramDataDir, dirent.Name()))
					if err != nil {
						t.Error(err)
						return
					}
					b, i := addDoc(dirent.Name(), doc, true)
					if i%100 == 0 {
						fmt.Println(i, bytes.Count(doc, []byte{'\n'}), b[0].Len(), b[0].load(),
							dirent.Name())
					}
					wg.Done()
				}
			}()
		}
		for _, dirent := range dirents {
			if dirent.IsDir() {
				continue
			}
			wg.Add(1)
			work <- dirent
		}
		close(work)
		wg.Wait()
	} else {
		if *docCount == 0 {
			*docCount = 4
		}
		for i := 0; i < *docCount; i++ {
			wordCount := 100 + rng.Intn(100)*rng.Intn(100)
			doc := []byte{}
			for j := 0; j < wordCount; j++ {
				doc = append(doc, randWord(4, 7, rng)...)
				doc = append(doc, '\n')
			}
			b, l := addDoc(fmt.Sprintf("%04d", i), doc, false)
			t.Log(l, wordCount, b[0].Len(), b[0].load())
		}
	}

	// sort docs by name for more deterministic output
	sort.Sort(&customSort{
		len:  func() int { return len(docs) },
		less: func(i, j int) bool { return docNames[i] < docNames[j] },
		swap: func(i, j int) {
			docNames[i], docNames[j] = docNames[j], docNames[i]
			docs[i], docs[j] = docs[j], docs[i]
			blooms[i], blooms[j] = blooms[j], blooms[i]
		},
	})

	t.Logf("loaded %d docs (%d MB / avg %d KB)", len(docs), docsize/1024/1024, docsize/len(docs)/1024)

	probes := [][]byte{}
	probeHashes := [][]uint32{}
	for _, doc := range docs {
		ws := bytes.Split(doc, []byte{'\n'})
		n := 0
		for _, w := range ws {
			if len(w) == 0 || len(w) > 20 {
				continue
			}
			if w[0] < '0' || w[0] > '9' {
				ws[n] = w
				n++
			}
		}
		ws = ws[:n]

		rng.Shuffle(len(ws), func(i, j int) {
			ws[i], ws[j] = ws[j], ws[i]
		})
		if len(ws) > 100 {
			ws = ws[:100]
		}
		if len(docs) > 1000 && len(ws) > 10 {
			ws = ws[:10]
		}
		probes = append(probes, ws...)
		for _, w := range ws {
			probeHashes = append(probeHashes, blooms[0][0].hasher(w))
		}
	}
	t.Logf("created %d probes", len(probes))

	fpCount := make([][]int, len(docs)) // false positive
	tpCount := make([][]int, len(docs)) // true positive
	tnCount := make([][]int, len(docs)) // true negative
	// false negative is impossible in bloom filters

	for i := 0; i < len(docs); i++ {
		fpCount[i] = make([]int, len(targetRate)+1)
		tpCount[i] = make([]int, len(targetRate)+1)
		tnCount[i] = make([]int, len(targetRate)+1)
	}

	work := make(chan int)

	for n := 0; n < cpuCount; n++ {
		wg.Add(1)
		go func() {
			for i := range work {
				// compute all ngrams that might be tested to reduce probing
				// time complexity from O(mn) to O(mlogn+nlogn)
				gram := make([]string, 0, len(docs[i])/8)
				// also compute trigrams for FPR baseling
				trigrams := map[string]bool{}
				for _, s := range bytes.Split(docs[i], []byte{'\n'}) {
					for i := 0; i <= len(s)-4; i++ {
						if '0' <= s[i] && s[i] <= '9' {
							continue
						}
						for j := i + 4; j < i+20 && j <= len(s); j++ {
							gram = append(gram, string(s[i:j]))
						}
						if *trigramFpr {
							trigrams[string(s[i:i+3])] = true
							trigrams[string(s[i+1:i+4])] = true
						}
					}
				}
				sort.Strings(gram)

				for j, w := range probes {
					gidx := -1
					trueValue := false

					if *trigramFpr {
						maybeTrigrams := true
						for wo := 0; wo < len(w)-3; wo++ {
							if !trigrams[string(w[wo:wo+3])] {
								maybeTrigrams = false
								break
							}
						}
						if maybeTrigrams {
							gidx = sort.SearchStrings(gram, string(w))
							trueValue = gidx >= 0 && gidx < len(gram) && gram[gidx] == string(w)
							if trueValue {
								tpCount[i][len(targetRate)]++
							} else {
								fpCount[i][len(targetRate)]++
							}
						} else {
							tnCount[i][len(targetRate)]++
						}
					}

					for bn, b := range blooms[i] {
						maybeHas := b.maybeHas(probeHashes[j])
						if maybeHas {
							if gidx == -1 {
								gidx = sort.SearchStrings(gram, string(w))
								trueValue = gidx >= 0 && gidx < len(gram) && gram[gidx] == string(w)
							}
							if trueValue {
								tpCount[i][bn]++
							} else {
								fpCount[i][bn]++
							}
						} else {
							tnCount[i][bn]++
						}
					}
				}
			}
			wg.Done()
		}()
	}

	for i := 0; i < len(docs); i++ {
		work <- i
	}
	close(work)
	wg.Wait()

	summer := make([]kahanSummer, len(targetRate)+1)
	for i := 0; i < len(docs); i++ {
		fprs := []string{}
		for bn := 0; bn < len(targetRate); bn++ {
			fpr := float64(fpCount[i][bn]) / float64(fpCount[i][bn]+tnCount[i][bn])
			if math.IsNaN(fpr) {
				fpr = 1
			}
			if fpr > 0.1 {
				t.Errorf("false positive rate %.04f > 0.01", fpr)
			}
			summer[bn].add(fpr)
			if *docFpr {
				fprs = append(fprs, fmt.Sprintf("%5.2f", 100*fpr))
			}
		}
		if *docFpr {
			fmt.Printf("doc: %4d bits: %8d fprs: %v name: %s\n", i, blooms[i][0].Len(), fprs[:len(targetRate)], docNames[i])
		}
	}
	t.Logf("hash=%s", hname)
	t.Log("load,fpr,avg size")
	for bn, rate := range targetRate {
		t.Logf("%d, %.03f, %d\n", rate, 100*summer[bn].avg(), totsize[bn]/8/len(docs))
	}
	if *trigramFpr {
		t.Logf("trigram fpr: %.03f\n", 100*summer[len(targetRate)].avg())
	}
}

type kahanSummer struct { // Kahan Summation
	sum float64
	c   float64
	n   int
}

func (k *kahanSummer) add(x float64) {
	y := x - k.c
	t := k.sum + y
	k.c = (t - k.sum) - y
	k.sum = t
	k.n++
}

func (k *kahanSummer) avg() float64 {
	return k.sum / float64(k.n)
}
