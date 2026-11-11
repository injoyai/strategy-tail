package extend

import "time"

// LastSuccessfulUpdate reads the batch marker from the configured market store.
func (p *PullKline) LastSuccessfulUpdate() (time.Time, error) {
	if p.Config.Store == nil {
		return time.Time{}, ErrMarketStoreRequired
	}
	timestamp, has, err := p.Config.Store.LoadUpdate("pull")
	if err != nil || !has || timestamp <= 0 {
		return time.Time{}, err
	}
	return time.Unix(timestamp, 0), nil
}
