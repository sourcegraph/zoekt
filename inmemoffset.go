package zoekt

type inMemoryNgrams map[ngram]simpleSection

func (m inMemoryNgrams) Get(gram ngram) simpleSection {
	ss, _ := m[gram]
	return ss
}

func (m inMemoryNgrams) DumpMap() map[ngram]simpleSection {
	return map[ngram]simpleSection(m)
}

func (m inMemoryNgrams) SizeBytes() int {
	return 0 // a bit complicated to calculate for real
}
