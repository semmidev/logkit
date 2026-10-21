// Package grpcmw wires logkit into a gRPC server: it propagates a
// request/correlation id via metadata, builds a wide event per RPC,
// recovers panics into a safe Internal status, and emits one canonical
// log line per call.
//
// This package is its own Go module (see go.mod next to this file)
// because it depends on google.golang.org/grpc, while the rest of
// logkit intentionally has zero third-party dependencies. Add it to
// your project with:
//
//	go get github.com/semmidev/logkit/middleware/grpcmw
//
// and register it like any other grpc interceptor:
//
//	srv := grpc.NewServer(
//	    grpc.ChainUnaryInterceptor(grpcmw.UnaryServerInterceptor(grpcmw.Config{Logger: logger})),
//	    grpc.ChainStreamInterceptor(grpcmw.StreamServerInterceptor(grpcmw.Config{Logger: logger})),
//	)
package grpcmw

import (
	"context"
	"time"

	"github.com/semmidev/logkit"
	"github.com/semmidev/logkit/event"
	"github.com/semmidev/logkit/logctx"
	"github.com/semmidev/logkit/sampler"
	"github.com/semmidev/logkit/secure"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Config controls the interceptors' behavior. Mirrors httpmw.Config.
type Config struct {
	// Logger is required.
	Logger *logkit.Logger

	// RequestIDMetadataKey is the incoming metadata key for the request
	// id (read-only here; outbound propagation to downstream services is
	// up to your client code). Defaults to "x-request-id". gRPC
	// lowercases metadata keys, so use lowercase here.
	RequestIDMetadataKey string

	// CorrelationIDMetadataKey defaults to "x-correlation-id". Read from
	// incoming metadata; falls back to the request id when absent.
	CorrelationIDMetadataKey string

	// Sampler decides which completed call events get logged. Defaults
	// to sampler.Always.
	Sampler sampler.Decider

	// GenerateID overrides how new ids are minted. Defaults to
	// logctx.NewID.
	GenerateID func() string
}

func (c *Config) setDefaults() {
	if c.RequestIDMetadataKey == "" {
		c.RequestIDMetadataKey = "x-request-id"
	}
	if c.CorrelationIDMetadataKey == "" {
		c.CorrelationIDMetadataKey = "x-correlation-id"
	}
	if c.Sampler == nil {
		c.Sampler = sampler.Always
	}
	if c.GenerateID == nil {
		c.GenerateID = logctx.NewID
	}
	if c.Logger == nil {
		c.Logger = logkit.NewNop()
	}
}

func idFromMetadata(ctx context.Context, key, fallback string, gen func() string) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(key); len(vals) > 0 && vals[0] != "" {
			return vals[0]
		}
	}
	if fallback != "" {
		return fallback
	}
	return gen()
}

func prepareContext(ctx context.Context, cfg Config, fullMethod string) (context.Context, *event.Event) {
	reqID := idFromMetadata(ctx, cfg.RequestIDMetadataKey, "", cfg.GenerateID)
	corrID := idFromMetadata(ctx, cfg.CorrelationIDMetadataKey, reqID, cfg.GenerateID)

	ctx = logctx.WithRequestID(ctx, reqID)
	ctx = logctx.WithCorrelationID(ctx, corrID)
	ctx = logctx.WithFields(ctx)

	ev := event.New()
	ev.Set("rpc.method", fullMethod)
	ctx = event.WithEvent(ctx, ev)
	return ctx, ev
}

func emit(ctx context.Context, cfg Config, ev *event.Event, code codes.Code) {
	snap := ev.Snapshot()
	hasError := snap["error"] != nil

	if !hasError && code == codes.OK && !cfg.Sampler.ShouldSample(snap) {
		return
	}

	msg := "rpc completed"
	switch {
	case hasError || code == codes.Internal || code == codes.Unknown:
		cfg.Logger.Error(ctx, msg, ev.Args()...)
	case code != codes.OK:
		cfg.Logger.Warn(ctx, msg, ev.Args()...)
	default:
		cfg.Logger.Info(ctx, msg, ev.Args()...)
	}
}

// UnaryServerInterceptor returns a grpc.UnaryServerInterceptor that logs
// one canonical line per RPC and recovers panics into codes.Internal
// (never leaking the panic value to the client, per fail-secure
// handling of an inconsistent-state condition).
func UnaryServerInterceptor(cfg Config) grpc.UnaryServerInterceptor {
	cfg.setDefaults()
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		start := time.Now()
		ctx, ev := prepareContext(ctx, cfg, info.FullMethod)

		defer func() {
			code := status.Code(err)
			if rec := recover(); rec != nil {
				se := secure.Wrap(secure.CodeInternal, "An internal error occurred.", panicToError(rec))
				ev.SetError(se)
				err = status.Error(codes.Internal, se.UserMsg)
				code = codes.Internal
			} else if err != nil {
				se, ok := secure.As(err)
				if !ok {
					se = secure.Internal(err)
				}
				ev.SetError(se)
			}
			ev.Set("rpc.code", code.String())
			ev.Set("rpc.duration_ms", float64(time.Since(start).Microseconds())/1000.0)
			emit(ctx, cfg, ev, code)
		}()

		resp, err = handler(ctx, req)
		return resp, err
	}
}

// StreamServerInterceptor is the streaming-RPC counterpart of
// UnaryServerInterceptor.
func StreamServerInterceptor(cfg Config) grpc.StreamServerInterceptor {
	cfg.setDefaults()
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		start := time.Now()
		ctx, ev := prepareContext(ss.Context(), cfg, info.FullMethod)
		wrapped := &wrappedStream{ServerStream: ss, ctx: ctx}

		defer func() {
			code := status.Code(err)
			if rec := recover(); rec != nil {
				se := secure.Wrap(secure.CodeInternal, "An internal error occurred.", panicToError(rec))
				ev.SetError(se)
				err = status.Error(codes.Internal, se.UserMsg)
				code = codes.Internal
			} else if err != nil {
				se, ok := secure.As(err)
				if !ok {
					se = secure.Internal(err)
				}
				ev.SetError(se)
			}
			ev.Set("rpc.code", code.String())
			ev.Set("rpc.duration_ms", float64(time.Since(start).Microseconds())/1000.0)
			emit(ctx, cfg, ev, code)
		}()

		err = handler(srv, wrapped)
		return err
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

func panicToError(rec any) error {
	if err, ok := rec.(error); ok {
		return err
	}
	return errString{rec}
}

type errString struct{ v any }

func (e errString) Error() string { return prettyAny(e.v) }

func prettyAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "panic: unrecoverable internal error"
}
