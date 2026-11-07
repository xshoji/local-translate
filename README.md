# local-translate

A local translation CLI for Japanese and English. No Ollama, inference server,
or API key required.

## Features

- **Works offline.** After downloading a model, translation runs entirely on your machine. Your text is not sent to a translation service.
- **No inference setup.** Run the single Go file or build a standalone executable. No Python, llama.cpp installation, or cgo required.
- **Automatic translation direction.** Japanese input becomes English; English input becomes Japanese.
- **Automatic model download.** On first use, review the size and save location, approve the download, and follow its progress.
- **Your choice of model.** Use the default Qwen3-4B or supply a compatible GGUF download URL.
- **Text, files, and pipelines.** Translate long documents with automatic splitting, and provide glossary terms, context, or Japanese writing style.

## Quick start

Requires Go 1.26+ to run or build. Tested on macOS with Apple Silicon.

From this directory:

```sh
go run main.go '会議は来週の月曜日に変更されました。'
go run main.go 'This system is convenient.'
```

The first translation asks permission to download the default model:

| Model | Parameters | Quantization | Download size |
| --- | --- | --- | --- |
| [Qwen3-4B](https://huggingface.co/Qwen/Qwen3-4B-GGUF) | 4B | Q4_K_M (4-bit) | ~2.50 GB |

Enter `y` or `yes` to continue. An interactive terminal and internet access are
required for the download; subsequent translations use the cached model offline.
Download size is not the amount of RAM needed to run the model.

To build a standalone executable:

```sh
CGO_ENABLED=0 go build -o bin/local-translate main.go
./bin/local-translate 'Hello, world.'
```

## Usage

Put options before the text. Only the translation is written to stdout, so it can
be saved or piped to another command.

```sh
# Translate stdin into polite Japanese
echo 'Please review the document.' | ./bin/local-translate -form polite

# Translate a file and save the result
./bin/local-translate -from en -to ja -file article.txt > translated.txt

# Provide context and preferred terminology
./bin/local-translate -context 'Project management help page.' \
  -glossary 'dashboard:管理画面' -glossary 'owner:担当者' \
  'Open the dashboard. You can filter tasks by owner.'
```

Use `-from` and `-to` for mixed-language text or other languages supported by your
model. Automatic direction uses Japanese characters as a hint, not full language
detection.

### Glossary and style

Repeat `-glossary 'source:target'` for multiple terms. Escape a literal colon as
`\:` and a backslash as `\\`; use single quotes to preserve the escapes.
For example, `-glossary 'aaa\:aaa:bbb\:bbb'` maps `aaa:aaa` to `bbb:bbb`.

Use `-form plain` or `-form polite` when translating into Japanese. Glossary and
style are instructions to the model, so adherence depends on the model.

### Long documents

Long input is split automatically and translated in order. Wording and formatting
may change across chunks. Each input must be valid UTF-8 and no larger than 4 MiB.
If any chunk fails or reaches its output limit before finishing, the command exits
with an error instead of writing a partial translation.

### Choosing a model

Use `-model-url` with a direct HTTP(S) download URL to a single GGUF file.
For example, this explicitly selects the default model:

```sh
./bin/local-translate -model-url \
  'https://huggingface.co/Qwen/Qwen3-4B-GGUF/resolve/bc640142c66e1fdd12af0bd68f40445458f3869b/Qwen3-4B-Q4_K_M.gguf' \
  'Hello, world.'
```

Choose a trusted publisher and review its license. Not every GGUF architecture or
chat template is supported by [go-llama](https://github.com/goccy/go-llama).
Split model files and HTML model pages are not supported.

The default model has a built-in checksum. For another URL, use `-sha256` with a
checksum supplied by the publisher to verify the download. Models are cached by
URL; changing the contents at the same URL does not update an existing cache entry.
Prefer URLs pinned to a specific revision.

### Model storage

Downloads show the exact destination before asking for approval. The cache root
is also shown by `-h` and follows the OS convention:

| OS | Cache directory |
| --- | --- |
| macOS | `~/Library/Caches/local-translate/` |
| Linux | `$XDG_CACHE_HOME/local-translate/`, or `~/.cache/local-translate/` |
| Windows | `%LocalAppData%\local-translate\` |

If the OS cache location cannot be determined, `./local-translate/` is used.

## Options

Run `./bin/local-translate -h` for help.

| Option | Purpose | Default |
| --- | --- | --- |
| `-model-url URL` | Direct GGUF download URL | Qwen3-4B Q4_K_M |
| `-sha256 HEX` | Expected model checksum | Built in for the default model |
| `-file PATH` | Read input from a file | Text arguments or stdin |
| `-from CODE` | Source language | Auto-detected |
| `-to CODE` | Target language | Automatic Japanese / English direction |
| `-form STYLE` | Japanese style: `plain` or `polite` | Unspecified |
| `-context TEXT` | Reference context | None |
| `-glossary source:target` | Preferred term pair; repeatable | None |
| `-ctx N` | Context tokens per chunk; maximum 32768 | 8192 |
| `-max-tokens N` | Output tokens per chunk | Half of `-ctx` |
| `-threads N` | CPU inference threads | Logical CPU count |
| `-template-file PATH` | Replace the entire prompt with a compatible Jinja template | Model's embedded template |
| `-debug` | Show model loading, chunk progress, and elapsed times on stderr | Off |
| `-h`, `--help` | Show help | — |

Larger context sizes require more RAM. If memory allocation fails, try `-ctx 2048`
or a smaller model. Inference uses the CPU, not Metal or CUDA; the engine has an
8 GiB memory cap, but total process memory can be higher.

### Environment variables

All options accept `LOCAL_TRANSLATE_` plus the uppercase option name, with `-`
replaced by `_`: for example, `-context` becomes `LOCAL_TRANSLATE_CONTEXT`.
Help uses `LOCAL_TRANSLATE_HELP`. CLI arguments override environment variables;
empty environment values are ignored.

```sh
LOCAL_TRANSLATE_CONTEXT='Project management help page.' \
  ./bin/local-translate 'Open the dashboard.'
```

For the glossary, supply one `source:target` pair per line. Any CLI `-glossary`
arguments replace the entire environment glossary.

## Development

Run tests with `go test ./...`. Tests that need real model weights skip when the
default model has not been downloaded.

For short- and long-text model comparisons, build `bin/local-translate`, download
the selected models through an interactive translation, then see
`python3 bench/compare.py --help`. Python is needed only for this optional script.

Review important translations against their source; model output can contain
omissions or changes in meaning.

## Release

The release flow for this repository is automated with GitHub Actions.
Pushing Git tags triggers the release job.

```
# Release
git tag 0.0.1 && git push --tags


# Delete tag
v="0.0.1"; git tag -d "${v}" && git push origin :"${v}"

# Delete tag and recreate new tag and push
v="0.0.1"; git tag -d "${v}" && git push origin :"${v}"; git tag "${v}"; git push --tags
```

## License

The CLI code is [MIT-licensed](LICENSE). go-llama is MIT-licensed; the Jinja
renderer is Apache-2.0-licensed. The
[default Qwen3-4B model is Apache-2.0-licensed](https://huggingface.co/Qwen/Qwen3-4B-GGUF/blob/bc640142c66e1fdd12af0bd68f40445458f3869b/LICENSE).

Model weights are not bundled and have separate licenses. Review the selected
model's requirements for use, redistribution, and publishing outputs. The CLI
does not append model-specific notices; include any required notices yourself.
