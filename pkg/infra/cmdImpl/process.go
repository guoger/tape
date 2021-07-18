package cmdImpl

import (
	"context"
	"fmt"
	"tape/pkg/infra/basic"
	"tape/pkg/infra/observer"
	"tape/pkg/infra/trafficGenerator"
	"time"

	"github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric-protos-go/peer"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

func Process(configPath string, num int, burst int, rate float64, logger *log.Logger) error {
	/*** variables ***/
	config, err := basic.LoadConfig(configPath)
	if err != nil {
		return err
	}
	crypto, err := config.LoadCrypto()
	if err != nil {
		return err
	}
	raw := make(chan *peer.Proposal, burst)
	signed := make([]chan *basic.Elements, len(config.Endorsers))
	processed := make(chan *basic.Elements, burst)
	envs := make(chan *common.Envelope, burst)
	blockCh := make(chan *observer.AddressedBlock)
	finishCh := make(chan struct{})
	errorCh := make(chan error, burst)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < len(config.Endorsers); i++ {
		signed[i] = make(chan *basic.Elements, burst)
	}
	/*** workers ***/

	blockCollector, err := observer.NewBlockCollector(config.CommitThreshold, len(config.Committers), ctx, blockCh, finishCh, num, true)
	if err != nil {
		return errors.Wrap(err, "failed to create block collector")
	}
	Initiator := &trafficGenerator.Initiator{Num: num, Burst: burst, R: rate, Config: config, Crypto: crypto, Raw: raw, ErrorCh: errorCh}
	assembler := &trafficGenerator.Assembler{Signer: crypto, Ctx: ctx, Raw: raw, Signed: signed, ErrorCh: errorCh}
	proposers, err := trafficGenerator.CreateProposers(ctx, signed, processed, config, logger)
	if err != nil {
		return err
	}
	Integrator := &trafficGenerator.Integrator{Signer: crypto, Ctx: ctx, Processed: processed, Envs: envs, ErrorCh: errorCh}
	broadcaster, err := trafficGenerator.CreateBroadcasters(ctx, envs, errorCh, config, logger)
	if err != nil {
		return err
	}

	observers, err := observer.CreateObservers(ctx, crypto, errorCh, blockCh, config, logger)
	if err != nil {
		return err
	}
	/*** start workers ***/

	proposers.Start()
	broadcaster.Start()

	go blockCollector.Start()
	go observers.Start()

	for i := 0; i < 5; i++ {
		go assembler.Start()
		go Integrator.Start()
	}

	go Initiator.Start()
	/*** waiting for complete ***/
	for {
		select {
		case err = <-errorCh:
			return err
		case <-finishCh:
			duration := time.Since(observers.StartTime)
			logger.Infof("Completed processing transactions.")
			fmt.Printf("tx: %d, duration: %+v, tps: %f\n", num, duration, float64(num)/duration.Seconds())
			return nil
		}
	}
}
