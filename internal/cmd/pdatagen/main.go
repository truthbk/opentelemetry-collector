// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/pdata"
)

// checkErr prints the given error and exits when e is non-nil.
func checkErr(e error) {
	if e != nil {
		fmt.Println(e)
		os.Exit(1)
	}
}

func main() {
	var workdir string
	flag.StringVar(&workdir, "C", ".", "set work directory")
	flag.Parse()

	checkErr(os.Chdir(workdir))
	// Path Y Phase 3: compute nestedPath on every reachable non-pcommon
	// messageStruct BEFORE generation, so templates can use the info
	// in Phase 4. Idempotent and side-effect-free for any consumer that
	// doesn't read nestedPath, so it's safe to enable unconditionally.
	for _, fp := range pdata.AllPackages {
		pdata.ComputeNestedPaths(fp)
	}
	checkErr(pdata.DeleteGeneratedFiles(filepath.Join("pdata", "internal")))
	for _, fp := range pdata.AllPackages {
		checkErr(pdata.DeleteGeneratedFiles(filepath.Join("pdata", fp.Path())))
		checkErr(fp.GenerateFiles())
		checkErr(fp.GenerateTestFiles())
		checkErr(fp.GenerateInternalFiles())
		checkErr(fp.GenerateProtoMessageFiles())
		checkErr(fp.GenerateProtoMessageTestsFiles())
		checkErr(fp.GenerateProtoEnumFiles())
	}
}
