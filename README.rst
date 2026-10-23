Twig, the flexible, fast, and secure template language, ported to Go
====================================================================

This repository holds a Go port of the core of Twig 3.30, the template
language. Twig uses a syntax similar to the Django and Jinja template
languages. The port covers the lexer, the parser and the evaluation of the
whole template language: every tag, filter, function, test and operator of the
core and escaper extensions, auto-escaping with the ``html``, ``js``, ``css``,
``url``, ``html_attr`` and ``name`` strategies, ``strict_variables``,
whitespace control, the filesystem loader, and the error messages with their
template name and line. Values follow PHP's semantics (loose comparison,
numeric strings, float printing, ``sprintf``, ``json_encode``, ``date()``), so
templates render exactly as they do with the PHP library.

The sandbox, the profiler, the debug extension, the string loader and the
packages in ``extra/`` are not part of the port.

Building
--------

The module uses the Go standard library only (Go 1.23 or later)::

    CGO_ENABLED=0 go build -o twig ./cmd/twig

Run the tests with::

    go test ./...

Usage
-----

::

    twig [options] TEMPLATE_DIR TEMPLATE_NAME [CONTEXT_FILE]

``twig`` loads ``TEMPLATE_NAME`` from ``TEMPLATE_DIR`` and renders it with the
JSON object in ``CONTEXT_FILE`` (``-`` reads standard input) as its context.
JSON objects become mappings and arrays become sequences, as PHP's
``json_decode($text, true)`` makes them. Options:

``--autoescape=STRATEGY``
    ``html`` (default), ``js``, ``css``, ``url``, ``html_attr``, ``name`` or
    ``false``.
``--strict-variables``
    Fail on undefined variables and attributes.
``--timezone=ZONE``
    Default timezone of the date filters and functions (default ``UTC``).
``--date-format=FORMAT``, ``--interval-format=FORMAT``
    Default formats of the ``date`` filter.
``--decimals=N``, ``--decimal-point=S``, ``--thousands-sep=S``
    Defaults of the ``number_format`` filter.
``--namespace NS=DIR``
    Adds a loader namespace (``@NS/name``); repeatable.
``--global NAME=JSON``
    Adds a global variable; repeatable.
``--block NAME``
    Renders a single block of the template.

The rendered output goes to standard output. Twig errors print
``<ErrorClass>: <message>`` on standard error and exit with status 2 (syntax),
3 (loader) or 4 (runtime); usage errors exit with 64, context errors with 65
or 66.

More Information
----------------

Read the `documentation`_ for the template language.

.. _documentation: https://twig.symfony.com/documentation
