package core

// StrategyCfg defines which relaying strategy to take for a given path
type StrategyCfg struct {
	Type string `json:"type" yaml:"type"`
}

// StrategyI defines
type StrategyI interface {
	GetType() string
	HandleEvents(src, dst ChainI, sh SyncHeadersI, events map[string][]string)
	UnrelayedSequences(src, dst ChainI, sh SyncHeadersI) (*RelaySequences, error)
	UnrelayedAcknowledgements(src, dst ChainI, sh SyncHeadersI) (*RelaySequences, error)
	RelayPackets(src, dst ChainI, sp *RelaySequences, sh SyncHeadersI) error
	RelayAcknowledgements(src, dst ChainI, sp *RelaySequences, sh SyncHeadersI) error
}

// RunStrategy runs a given strategy
func RunStrategy(src, dst ChainI, strategy StrategyI) (func(), error) {
	doneChan := make(chan struct{})

	// Fetch latest headers for each chain and store them in sync headers
	sh, err := NewSyncHeaders(src, dst)
	if err != nil {
		return nil, err
	}

	// Next start the goroutine that listens to each chain for block and tx events
	go src.StartEventListener(dst, strategy)
	go dst.StartEventListener(src, strategy)

	// Fetch any unrelayed sequences depending on the channel order
	sp, err := strategy.UnrelayedSequences(src, dst, sh)
	if err != nil {
		return nil, err
	}

	if err = strategy.RelayPackets(src, dst, sp, sh); err != nil {
		return nil, err
	}

	// Return a function to stop the relayer goroutine
	return func() { doneChan <- struct{}{} }, nil
}