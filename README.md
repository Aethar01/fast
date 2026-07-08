<p align="center">
	<img src="vhs/demo.gif" alt="VHS recording of fast">
	<a href="https://vhs.charm.sh">
		<img src="https://stuff.charm.sh/vhs/badge.svg">
	</a>
</p>

# Faster - fork of Fast

This repo is a fork of [fast](https://github.com/maaslalani/fast)

Test your internet speed from the command-line, powered by [fast.com](https://fast.com).

### Usage

```
Usage:
  fast [flags]

Flags:
  -client
        Show client info
  -connections connections
        Number of parallel connections (default 8)
  -download
        Measure download speed
  -duration duration
        Measure each transfer for duration (default 10s)
  -h    Show help
  -help
        Show help
  -ipv4
        Prefer IPv4 targets
  -ipv6
        Prefer IPv6 targets
  -json
        Print results as JSON
  -no-tui
        Print plain text instead of the terminal UI
  -server
        Show server info
  -token token
        Use an explicit fast.com API token
  -upload
        Measure upload speed
  -version
        Show version

Keyboard:
  q, esc, ctrl+c    Quit
```

`fast` measures your upload and download speed against the nearest Netflix Open Connect
servers and reports it in megabits per second, right inline in your terminal,
along with your ping to that server.

## License

[MIT](https://github.com/maaslalani/fast/blob/master/LICENSE)
