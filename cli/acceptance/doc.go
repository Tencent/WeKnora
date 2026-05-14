// cli/acceptance/doc.go
//
// Package acceptance is the cli/ subtree top-level home for cross-cutting
// contract / integration tests. Lives outside cli/internal/ so a future
// reviewer immediately sees "this is the contract surface - change with care".
//
// Sub-packages:
//   contract/    - envelope JSON shape golden + error.code registry consistency
//   e2e/         - real WeKnora server blackbox tests (testscript-style)
package acceptance
