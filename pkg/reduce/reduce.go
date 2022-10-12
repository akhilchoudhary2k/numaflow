package reduce

import (
	"context"
	"github.com/numaproj/numaflow/pkg/isb"
	"github.com/numaproj/numaflow/pkg/isb/forward"
	"github.com/numaproj/numaflow/pkg/pbq"
	"github.com/numaproj/numaflow/pkg/reduce/readloop"
	"github.com/numaproj/numaflow/pkg/shared/logging"
	udfReducer "github.com/numaproj/numaflow/pkg/udf/reducer"
	"github.com/numaproj/numaflow/pkg/watermark/fetch"
	"github.com/numaproj/numaflow/pkg/watermark/publish"
	"github.com/numaproj/numaflow/pkg/window"
	"go.uber.org/zap"
	"time"
)

// read from the isb
// attach watermark to read messages
// invoke the read-loop with the read messages

type DataForward struct {
	fromBuffer        isb.BufferReader
	readloop          *readloop.ReadLoop
	fetchWatermark    fetch.Fetcher
	windowingStrategy window.Windower
	opts              *Options
	log               *zap.SugaredLogger
}

func NewDataForward(ctx context.Context,
	udf udfReducer.Reducer,
	fromBuffer isb.BufferReader,
	pbqManager *pbq.Manager,
	toBuffers map[string]isb.BufferWriter,
	whereToDecider forward.ToWhichStepDecider,
	fw fetch.Fetcher,
	publishWatermark map[string]publish.Publisher,
	windowingStrategy window.Windower, opts ...Option) (*DataForward, error) {

	options := DefaultOptions()

	for _, opt := range opts {
		if err := opt(options); err != nil {
			return nil, err
		}
	}

	rl := readloop.NewReadLoop(ctx, udf, pbqManager, windowingStrategy, toBuffers, whereToDecider, publishWatermark, options.windowOpts)
	return &DataForward{
		fromBuffer:        fromBuffer,
		readloop:          rl,
		fetchWatermark:    fw,
		windowingStrategy: windowingStrategy,
		log:               logging.FromContext(ctx),
		opts:              options}, nil
}

func (d *DataForward) Start(ctx context.Context) {
	d.readloop.Startup(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			d.forwardAChunk(ctx)
		}
	}
}

func (d *DataForward) forwardAChunk(ctx context.Context) {
	readMessages, err := d.fromBuffer.Read(ctx, d.opts.readBatchSize)

	if err != nil {
		d.log.Errorw("failed to read from isb", zap.Error(err))
	}
	if len(readMessages) == 0 {
		return
	}

	// fetch watermark if available
	// let's track only the last element's watermark
	processorWM := d.fetchWatermark.GetWatermark(readMessages[len(readMessages)-1].ReadOffset)
	for _, m := range readMessages {
		m.Watermark = time.Time(processorWM)
	}

	d.readloop.Process(ctx, readMessages)
}
