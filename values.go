// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import "github.com/twigphp/twig/internal/php"

// toStr converts a value to a string as PHP's string conversion does, ignoring conversion errors (used for
// messages, where PHP would print the same text).
func toStr(v any) string { return php.MustString(v) }

// truthy is PHP's boolean conversion.
func truthy(v any) bool { return php.ToBool(v) }

// looseEqualsNumberString is PHP's `$number == "string"` for Token::test on number tokens.
func looseEqualsNumberString(n any, s string) bool { return php.Equal(n, s) }
