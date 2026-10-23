// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

// Command twig renders one template of a template directory against a JSON context.
//
//	twig [options] TEMPLATE_DIR TEMPLATE_NAME [CONTEXT_FILE]
package main

import (
	"os"

	"github.com/twigphp/twig/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
