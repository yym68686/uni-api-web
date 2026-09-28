package main

import "errors"

// No page-count ceiling: a broken upstream that repeats pages still terminates
// without claiming a partial scan is complete or creating a duplicate key.
type keyPageProgress map[int64]bool

func (seen keyPageProgress) advance(keys []subRemoteKey) error {
	progress := len(keys) == 0
	for _, key := range keys {
		if key.ID <= 0 {
			return errors.New("invalid key identity")
		}
		if !seen[key.ID] {
			progress = true
			seen[key.ID] = true
		}
	}
	if !progress {
		return errors.New("key pagination did not advance")
	}
	return nil
}
