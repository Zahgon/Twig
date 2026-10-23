// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/twigphp/twig/internal/php"
)

// Options are the environment options the command line exposes.
type Options struct {
	// Autoescape is the default strategy: "html", "js", "css", "url", "html_attr", "name" (from the template name)
	// or "" for false.
	Autoescape      string
	StrictVariables bool
	Timezone        string
	DateFormat      string
	IntervalFormat  string
	Decimals        int64
	DecimalPoint    string
	ThousandsSep    string
}

// Environment is Twig\Environment with a FilesystemLoader.
type Environment struct {
	Options
	loader    *FilesystemLoader
	globals   *php.Array
	templates map[string]*Template
	loading   map[string]bool
}

// NewEnvironment returns an environment with the core defaults.
func NewEnvironment(loader *FilesystemLoader, opts Options) *Environment {
	return &Environment{Options: opts, loader: loader, globals: php.NewArray(), templates: map[string]*Template{}}
}

// AddGlobal is Environment::addGlobal.
func (env *Environment) AddGlobal(name string, value any) { env.globals.Set(name, value) }

// FilesystemLoader is Twig\Loader\FilesystemLoader.
type FilesystemLoader struct {
	rootPath   string
	paths      map[string][]string
	nsOrder    []string
	cache      map[string]string
	errorCache map[string]string
}

// NewFilesystemLoader is new FilesystemLoader([], rootPath).
func NewFilesystemLoader(rootPath string) *FilesystemLoader {
	l := &FilesystemLoader{paths: map[string][]string{}, cache: map[string]string{}, errorCache: map[string]string{}}
	if real, err := filepath.EvalSymlinks(rootPath); err == nil {
		if abs, err := filepath.Abs(real); err == nil {
			rootPath = abs
		}
	}
	l.rootPath = rootPath + "/"
	return l
}

var urlScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func isAbsolutePath(file string) bool {
	if file != "" && (file[0] == '/' || file[0] == '\\') {
		return true
	}
	if len(file) > 3 && isAlpha(file[0]) && file[1] == ':' && (file[2] == '/' || file[2] == '\\') {
		return true
	}
	// parse_url($file, PHP_URL_SCHEME) !== null
	return urlScheme.MatchString(file) && !strings.HasPrefix(file, "//")
}

// AddPath is FilesystemLoader::addPath.
func (l *FilesystemLoader) AddPath(path, namespace string) error {
	l.cache = map[string]string{}
	l.errorCache = map[string]string{}
	check := path
	if !isAbsolutePath(path) {
		check = l.rootPath + path
	}
	if st, err := os.Stat(check); err != nil || !st.IsDir() {
		return newLoaderError(sprintf(`The "%s" directory does not exist ("%s").`, path, check))
	}
	if _, ok := l.paths[namespace]; !ok {
		l.nsOrder = append(l.nsOrder, namespace)
	}
	l.paths[namespace] = append(l.paths[namespace], strings.TrimRight(path, `/\`))
	return nil
}

var multiSlash = regexp.MustCompile(`/{2,}`)

func (l *FilesystemLoader) normalizeName(name string) string {
	return multiSlash.ReplaceAllString(strings.ReplaceAll(name, `\`, "/"), "/")
}

// findTemplate returns the file of a template, or a LoaderError.
func (l *FilesystemLoader) findTemplate(name string) (string, *Error) {
	name = l.normalizeName(name)
	if p, ok := l.cache[name]; ok {
		return p, nil
	}
	if msg, ok := l.errorCache[name]; ok {
		return "", newLoaderError(msg)
	}
	namespace, short := "__main__", name
	if name != "" && name[0] == '@' {
		i := strings.IndexByte(name, '/')
		if i < 0 {
			return "", newLoaderError(sprintf(`Malformed namespaced template name "%s" (expecting "@namespace/template_name").`, name))
		}
		namespace, short = name[1:i], name[i+1:]
	}
	if strings.Contains(short, "\x00") {
		return "", newLoaderError("A template name cannot contain NUL bytes.")
	}
	trimmed := strings.TrimLeft(short, "/")
	level := 0
	for _, part := range strings.Split(trimmed, "/") {
		if part == ".." {
			level--
		} else if part != "." {
			level++
		}
		if level < 0 {
			return "", newLoaderError(sprintf(`Looks like you try to load a template outside configured directories (%s).`, trimmed))
		}
	}
	paths, ok := l.paths[namespace]
	if !ok {
		msg := sprintf(`There are no registered paths for namespace "%s".`, namespace)
		l.errorCache[name] = msg
		return "", newLoaderError(msg)
	}
	for _, path := range paths {
		if !isAbsolutePath(path) {
			path = l.rootPath + path
		}
		file := path + "/" + short
		if st, err := os.Stat(file); err == nil && st.Mode().IsRegular() {
			if real, err := filepath.EvalSymlinks(file); err == nil {
				if abs, err := filepath.Abs(real); err == nil {
					l.cache[name] = abs
					return abs, nil
				}
			}
			l.cache[name] = file
			return file, nil
		}
	}
	msg := sprintf(`Unable to find template "%s" (looked into: %s).`, name, strings.Join(paths, ", "))
	l.errorCache[name] = msg
	return "", newLoaderError(msg)
}

func (l *FilesystemLoader) exists(name string) bool {
	_, err := l.findTemplate(name)
	return err == nil
}

// cacheKey is FilesystemLoader::getCacheKey: the file relative to the root path.
func (l *FilesystemLoader) cacheKey(file string) string {
	if strings.HasPrefix(file, l.rootPath) {
		return file[len(l.rootPath):]
	}
	return file
}

// load is Environment::load / loadTemplate: the compiled template of a name, with its parse and compile errors.
func (env *Environment) load(name string) *Template {
	file, lerr := env.loader.findTemplate(name)
	if lerr != nil {
		panic(lerr)
	}
	key := env.loader.cacheKey(file)
	if t, ok := env.templates[key]; ok {
		return t
	}
	code, err := os.ReadFile(file)
	if err != nil {
		panic(newLoaderError(sprintf(`Unable to find template "%s".`, name)))
	}
	src := &Source{Code: string(code), Name: name, Path: file}
	mod := env.compileSource(src)
	t := env.newTemplate(mod, nil)
	env.templates[key] = t
	t.construct()
	return t
}

// compileSource tokenizes, parses and checks a template (Environment::compileSource).
func (env *Environment) compileSource(src *Source) (mod *Module) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*Error); ok {
				e.Source = src
				if e.Line == 0 {
					e.Line = -1
				}
			}
			panic(r)
		}
	}()
	stream, err := Tokenize(src)
	if err != nil {
		panic(err)
	}
	p := &Parser{env: env}
	mod = p.parse(stream, nil, false)
	env.compileModule(mod)
	return mod
}
