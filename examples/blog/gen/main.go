// Command gen regenerates the blogdb bindings from the dsl design in
// ../design. Run from examples/blog:
//
//	go run ./gen
package main

import (
	"log"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/dsl"
	"github.com/aita/sqlkit/examples/blog/design"
)

func main() {
	files, err := design.Blog.Generate(codegen.Options{PackageName: "blogdb"})
	if err != nil {
		log.Fatal(err)
	}
	if err := dsl.Write("blogdb", files); err != nil {
		log.Fatal(err)
	}
}
