// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var counter instrument.Int64Counter

func main() {
	ctx := context.Background()
	// set up traceExporter
	traceExporter, err := otlptrace.New(ctx,
		otlptracegrpc.NewClient(otlptracegrpc.WithInsecure()),
	)
	if err != nil {
		log.Fatalf("Error creating trace exporter: %s", err)
	}

	// set up tracerprovider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(traceExporter)),
	)
	defer tp.Shutdown(ctx)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// set up metrics exporter and meter provider
	exporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		log.Fatalf("Error creating exporter: %s", err)
	}
	provider := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(exporter)),
	)
	defer provider.Shutdown(ctx)
	meter := provider.Meter("example.com/metrics")
	counter, err = meter.Int64Counter("sidecar-sample-counter")
	if err != nil {
		log.Fatalf("Error creating counter: %s", err)
	}

	http.HandleFunc("/", handler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func traceLogPrefix(traceId, spanId string) string {
	return fmt.Sprintf("sample-app [%s][spanId: %s]: ", traceId, spanId)
}

func handler(w http.ResponseWriter, r *http.Request) {
	// get trace context propagated from http request
	prop := otel.GetTextMapPropagator()
	ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	tp := otel.GetTracerProvider()
	tracer := tp.Tracer("example.com/trace")
	ctx, span := tracer.Start(ctx, "foo")
	defer span.End()

	// extract current span ID
	spanId := span.SpanContext().SpanID().String()
	traceId := span.SpanContext().TraceID().String()

	// log incoming request with spanID
	logger := log.New(os.Stdout, traceLogPrefix(traceId, spanId), log.LstdFlags)
	logger.Printf("Request: %s %s", r.Method, r.URL.Path)
	fmt.Fprintln(w, "Logged request to /logging/sample-app.log")

	// write traces
	generateSpans(ctx, tracer, logger, 10)
	fmt.Fprintln(w, "Generated 10 spans!")

	// update metric
	counter.Add(ctx, 100)
	fmt.Fprintln(w, "Updated sidecar-sample-counter metric!")
}

func generateSpans(ctx context.Context, tracer trace.Tracer, logger *log.Logger, id int) {
	if id > 0 {
		ctx, span := tracer.Start(ctx, fmt.Sprintf("foo-%d", id))
		defer span.End()
		logger.SetPrefix(traceLogPrefix(
			span.SpanContext().TraceID().String(),
			span.SpanContext().SpanID().String(),
		))
		logger.Printf("Generating span %d...\n", id)
		generateSpans(ctx, tracer, logger, id-1)
	} else {
		fmt.Println("Done.")
	}
}
