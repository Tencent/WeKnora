// Package docparser turns uploaded files and URLs into Markdown plus the
// images they contain, which is the input the chunking and multimodal stages
// downstream expect.
//
// A parse request names a parser engine, and every engine is one entry in the
// registry:
//
//   - engines.go declares the catalog — each engine's name, description,
//     supported file types, availability probe, and the reader that does the
//     work.
//   - engine_registry.go turns an engine name into a reader (NewReader) and
//     merges the local catalog with the engines the Python docreader reports
//     over its ListEngines RPC (ListAllEngines).
//
// Readers differ in where the parsing happens: in this process
// (SimpleFormatReader, AnydocReader), in the docreader service over gRPC or
// HTTP (GRPCDocumentReader, HTTPDocumentReader), or in a remote API (MinerU,
// PaddleOCR-VL, WeKnora Cloud). They all return types.ReadResult, so the rest
// of the package — image resolution and storage, table normalization — is
// shared regardless of which engine ran.
package docparser
