# Unlurker

[![Release](https://img.shields.io/github/v/release/jasonthorsness/unlurker?label=release&style=flat-square)](https://github.com/jasonthorsness/unlurker/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/jasonthorsness/unlurker)](https://goreportcard.com/report/github.com/jasonthorsness/unlurker)
[![License](https://img.shields.io/github/license/jasonthorsness/unlurker?style=flat-square)](https://github.com/jasonthorsness/unlurker/blob/main/LICENSE)
[![CI](https://github.com/jasonthorsness/unlurker/actions/workflows/ci.yml/badge.svg)](https://github.com/jasonthorsness/unlurker/actions)

Unlurker helps you find the liveliest discussions on
[news.ycombinator.com](https://news.ycombinator.com) so you can jump in before discussion dies.

As seen on [hn.unlurker.com](https://hn.unlurker.com)!

This repo releases two tools and a client library for the
[HN API](https://github.com/HackerNews/API).

| Tool | Purpose                       |
| ---- | ----------------------------- |
| unl  | Find active HN discussions    |
| hn   | Retrieve data from the HN API |

## Installation

Install on Linux or Mac OS to `/usr/local/bin` (requires `curl`, `tar`, and `sudo`).

### unl

```bash
curl -Ls "https://github.com/jasonthorsness/unlurker/releases/latest/download/unl_\
$(uname -s | tr '[:upper:]' '[:lower:]')_\
$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')\
.tar.gz" \
| sudo tar -xz -C /usr/local/bin
unl --limit 1
```

### hn

```bash
curl -Ls "https://github.com/jasonthorsness/unlurker/releases/latest/download/hn_\
$(uname -s | tr '[:upper:]' '[:lower:]')_\
$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')\
.tar.gz" \
| sudo tar -xz -C /usr/local/bin
hn new --limit 1
```

## Quick Start

`unl`

```bash
# Show the 3 latest stories with active discussions
unl --limit 3

# Show stories with activity from at least 10 unique users in the last hour max 24 hours old
unl --min-by 10 --window 1h --max-age 24h
```

`hn`

```bash
# Find stories from the 'new' list with 'rust' in the title
hn new | grep rust

# Download the latest 10000 records to out.json (with resume support)
hn scan -l10000 -c- -o out.json
```

## Usage

Both tools have some common flags related to persistent caching.

| flag                | purpose                                    |
| ------------------- | ------------------------------------------ |
| --cache-path string | override the default persistent cache path |
| --no-cache          | disable persistent caching                 |

Since most stories and comments rarely change, both tools maintain a shared persistent cache of
retrieved content. Items will be retrieved from the cache until deemed stale. How long it takes for
an item to be considered stale depends on the cached item's age, starting at one minute and reaching
immutable for items older than a couple of weeks.

This persistent cache file defaults to `hn.db` stored in the user-specific cache or global temp
directory. To see the default storage location for your machine, just run the tool with `--help` and
note the default for `--cache-path`.

To disable this persistent caching, use `--no-cache`. To change the location use `--cache-path`.

### `unl` usage

```text
unl finds active discussions on news.ycombinator.com

Usage:
  unl [flags]

Examples:
  unl --max-age 8h --window 30m --min-by 3 --limit 3

Flags:
      --cache-path string   cache file path (default "/home/jason/.cache/hn.db")
  -h, --help                help for unl
  -l, --limit int           limit the number of results
      --max-age duration    maximum age for items (default 24h0m0s)
      --min-by int          minimum count of unique contributors to activity (default 3)
      --no-cache            disable cache
      --no-color            disable color
      --window duration     time window for activity (default 1h0m0s)
```

#### `unl` sample output

`unl` works best in wide terminals because it doesn't wrap text. The sample output below is 100
characters. It shows the full links because most terminals make them clickable and navigating to the
discussions is the point of the tool.

```text
https://news.ycombinator.com/item?id=43740065       schappim 2h13m Ask HN: What Did You Learn Too …
https://news.ycombinator.com/item?id=43740647  hiAndrewQuinn   15m |\- Everyone else is giving vag…
https://news.ycombinator.com/item?id=43740589  WheelsAtLarge   26m |\- You need to make sure manag…
https://news.ycombinator.com/item?id=43740601      mindcrime   23m | \- To add to that: there may …
https://news.ycombinator.com/item?id=43740586      mindcrime   27m  \- First of all, I don't think…
```

### `hn` usage

```text
hn retrieves data from the HN API (https://github.com/HackerNews/API)

Usage:
  hn [command] [flags]
  hn [command]

Examples:
  hn new --limit 3
  hn user jasonthorsness --submitted --limit 5
  hn scan --limit 10000 --continue-at - -o out.json

Available Commands:
  best        Retrieve items from the best list
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  new         Retrieve items from the new list
  scan        Retrieve a range of items from the HN API
  top         Retrieve items from the top list
  user        Retrieve a user's profile or their submitted items

Flags:
      --cache-path string     cache file path (default "/home/jason/.cache/hn.db")
  -h, --help                  help for hn
      --max-connections int   maximum TCP connections to open (default 100)
      --no-cache              disable caching
  -o, --output string         output filename

Use "hn [command] --help" for more information about a command.
```

#### `hn` sample output

`hn` outputs JSON exactly as the HN API returns it. If you want to filter or pretty-print the JSON,
use a tool like `jq`.

```text
{"by":"leonewton253","descendants":1,"id":43740739,"kids":[43740740],"score":1,"time":1745110876,"title":"SteamOS: Nix Edition. First Beta Release","type":"story","url":"https://github.com/SteamNix/SteamNix"}
```

#### `hn scan` notes

The `scan` command can be used to download the entire HN database. Since this can take quite some
time, make sure you use the `--continue-at -` option with an output file `-o out.json`. If you do
this, and something goes wrong (or you simply CTRL+C for some reason), you will be able to
re-execute the same command and it will look at the contents of out.json to figure out where to
correctly resume. You can resume with a different limit and cache settings.

## Client Library

You'll need to be using at least go 1.24.1.

```bash
go get github.com/jasonthorsness/unlurker/hn
```

For a simple example of using the client library, refer to [cmd/unl/main.go](cmd/unl/main.go).

## Building

This project requires the go 1.24.1 SDK. Run 'make' to build both tools.
