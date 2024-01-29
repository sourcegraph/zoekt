package query

type ExperimentalPhraseBoostOptions struct {
	// The phrase needs to contain atleast this many terms. This is based on the
	// parsed query.
	//
	// Defaults to 3.
	MinTerms int

	// Boost is how much to multiply the phrase match scores by.
	//
	// Defaults to 20.
	Boost float64
}

// ExperimentalPhraseBoost transforms q into a query containing exact matches
// to phrase boosted. opts control how and when the boosting is done.
//
// Note: This is a temporary API and will be removed in future commits.
func ExpirementalPhraseBoost(q Q, phrase string, opts ExperimentalPhraseBoostOptions) Q {
	if opts.MinTerms == 0 {
		opts.MinTerms = 3
	}
	if opts.Boost == 0 {
		opts.Boost = 20
	}

	contentAtoms := 0
	caseSensitive := false
	VisitAtoms(q, func(q Q) {
		switch s := q.(type) {
		case *Regexp:
			// Check atom is for content
			if s.Content || (s.Content == s.FileName) {
				caseSensitive = s.CaseSensitive
				contentAtoms++
			}
		case *Substring:
			if s.Content || (s.Content == s.FileName) {
				caseSensitive = s.CaseSensitive
				contentAtoms++
			}
		}
	})

	if contentAtoms < opts.MinTerms {
		return q
	}

	return NewOr(
		&Boost{
			Boost: opts.Boost,
			Child: &Substring{
				Pattern:       phrase,
				Content:       true,
				CaseSensitive: caseSensitive,
			},
		},
		q,
	)
}
