package stats

import "time"

type StatsConfig struct {
	SnapshotInterval time.Duration
	SnapshotCallback func(s *Stats)
}

type Stats struct {
	LiveConn         int64
	TotalConn        int64
	MessagesSent     int64
	MessagesReceived int64
	ErrorCount       int64

	ticker   *time.Ticker
	callback func(s *Stats)
}

func NewStatsWithConfig(c StatsConfig) *Stats {
	return &Stats{
		ticker:   time.NewTicker(c.SnapshotInterval),
		callback: c.SnapshotCallback,
	}
}

func (s *Stats) Start() {
	for {
		<-s.ticker.C
		s.callback(s)
	}
}

func (s *Stats) Stop() {
	s.ticker.Stop()
}
