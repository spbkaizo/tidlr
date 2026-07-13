package tidal

// similarityRatio computes a string similarity in [0,1] equivalent to Python's
// difflib.SequenceMatcher(None, a, b).ratio(), which the original matcher used.
// The ratio is 2*M / T where M is the total number of matched characters found
// by the Ratcliff/Obershelp recursive longest-matching-block algorithm and T is
// the combined length of both strings.
func similarityRatio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	total := len(ra) + len(rb)
	if total == 0 {
		return 1.0
	}
	matches := matchingBlocks(ra, rb)
	return 2.0 * float64(matches) / float64(total)
}

// matchingBlocks returns the total number of matched characters using the
// recursive longest-common-contiguous-block decomposition (Ratcliff/Obershelp),
// the same decomposition difflib uses for ratio().
func matchingBlocks(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// Find the longest matching contiguous block between a and b.
	bestI, bestJ, bestLen := longestMatch(a, b)
	if bestLen == 0 {
		return 0
	}
	// Recurse on the segments to the left and right of the block.
	return matchingBlocks(a[:bestI], b[:bestJ]) +
		bestLen +
		matchingBlocks(a[bestI+bestLen:], b[bestJ+bestLen:])
}

// longestMatch finds the longest contiguous matching run between a and b,
// returning its start indices and length. On ties it prefers the earliest
// match in a, then in b (matching difflib's behavior).
func longestMatch(a, b []rune) (starti, startj, length int) {
	// j2len[j] = length of the longest match ending at a[i-1], b[j-1].
	j2len := make(map[int]int)
	for i := 0; i < len(a); i++ {
		newj2len := make(map[int]int)
		for j := 0; j < len(b); j++ {
			if a[i] != b[j] {
				continue
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > length {
				starti = i - k + 1
				startj = j - k + 1
				length = k
			}
		}
		j2len = newj2len
	}
	return starti, startj, length
}
