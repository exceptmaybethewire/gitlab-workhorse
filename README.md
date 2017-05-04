# gitlab-workhorse

Gitlab-workhorse is a smart reverse proxy for GitLab. It handles
"large" HTTP requests such as file downloads, file uploads, Git
push/pull and Git archive downloads.

## Quick facts (how does Workhorse work)

-   Workhorse can handle some requests without involving Rails at all:
    for example, Javascript files and CSS files are served straight
    from disk.
-   Workhorse can modify responses sent by Rails: for example if you use
    `send_file` in Rails then gitlab-workhorse will open the file on
    disk and send its contents as the response body to the client.
-   Workhorse can take over requests after asking permission from Rails.
    Example: handling `git clone`.
-   Workhorse can modify requests before passing them to Rails. Example:
    when handling a Git LFS upload Workhorse first asks permission from
    Rails, then it stores the request body in a tempfile, then it sends
    a modified request containing the tempfile path to Rails.
-   Workhorse can manage long-lived WebSocket connections for Rails.
    Example: handling the terminal websocket for environments.
-   Workhorse does not connect to Postgres, only to Rails and (optionally) Redis.
-   We assume that all requests that reach Workhorse pass through an
    upstream proxy such as NGINX or Apache first.
-   Workhorse does not accept HTTPS connections.
-   Workhorse does not clean up idle client connections.
-   We assume that all requests to Rails pass through Workhorse.

For more information see ['A brief history of
gitlab-workhorse'][brief-history-blog].

## Usage

```
  gitlab-workhorse [OPTIONS]

Options:
  -apiCiLongPollingDuration duration
        Long polling duration for job requesting for runners (default 0s - disabled)
  -apiLimit uint
        Number of API requests allowed at single time
  -apiQueueDuration duration
        Maximum queueing duration of requests (default 30s)
  -apiQueueLimit uint
        Number of API requests allowed to be queued
  -authBackend string
    	Authentication/authorization backend (default "http://localhost:8080")
  -authSocket string
    	Optional: Unix domain socket to dial authBackend at
  -developmentMode
    	Allow to serve assets from Rails app
  -documentRoot string
    	Path to static files content (default "public")
  -listenAddr string
    	Listen address for HTTP server (default "localhost:8181")
  -listenNetwork string
    	Listen 'network' (tcp, tcp4, tcp6, unix) (default "tcp")
  -listenUmask int
    	Umask for Unix socket
  -objectStoreTimeout duration
    	Timeout for object storage uploads (default 6m0s)
  -pprofListenAddr string
    	pprof listening address, e.g. 'localhost:6060'
  -proxyHeadersTimeout duration
    	How long to wait for response headers when proxying the request (default 5m0s)
  -secretPath string
    	File with secret key to authenticate with authBackend (default "./.gitlab_workhorse_secret")
  -config string
    	File that hold configuration. Currently only for redis. File is in TOML-format (default "")
  -version
    	Print version and exit
```

The 'auth backend' refers to the GitLab Rails application. The name is
a holdover from when gitlab-workhorse only handled Git push/pull over
HTTP.

Gitlab-workhorse can listen on either a TCP or a Unix domain socket. It
can also open a second listening TCP listening socket with the Go
[net/http/pprof profiler server](http://golang.org/pkg/net/http/pprof/).

Gitlab-workhorse can listen on redis events (currently only builds/register
for runners). This requires you to pass a valid TOML config file via
`-config` flag.  
For regular setups it only requires the following (replacing the string 
with the actual socket)

### Redis

Gitlab-workhorse integrates with Redis to do long polling for CI build
requests. This is configured via two things:

-   Redis settings in the TOML config file
-   The `-apiCiLongPollingDuration` command line flag to control polling
    behavior for CI build requests

It is OK to enable Redis in the config file but to leave CI polling
disabled; this just results in an idle Redis pubsub connection. The
opposite is not possible: CI long polling requires a correct Redis
configuration.

Below we discuss the options for the `[redis]` section in the config
file.

```
[redis]
URL = "unix:///var/run/gitlab/redis.sock"
Password = "my_awesome_password"
Sentinel = [ "tcp://sentinel1:23456", "tcp://sentinel2:23456" ]
SentinelMaster = "mymaster"
```

- `URL` takes a string in the format `unix://path/to/redis.sock` or
`tcp://host:port`.
- `Password` is only required if your redis instance is password-protected
- `Sentinel` is used if you are using Sentinel.
  *NOTE* that if both `Sentinel` and `URL` are given, only `Sentinel` will be used

Optional fields are as follows:
```
[redis]
DB = 0
ReadTimeout = "1s"
KeepAlivePeriod = "5m"
MaxIdle = 1
MaxActive = 1
```

- `DB` is the Database to connect to. Defaults to `0`
- `ReadTimeout` is how long a redis read-command can take. Defaults to `1s`
- `KeepAlivePeriod` is how long the redis connection is to be kept alive without anything flowing through it. Defaults to `5m`
- `MaxIdle` is how many idle connections can be in the redis-pool at once. Defaults to 1
- `MaxActive` is how many connections the pool can keep. Defaults to 1

### Relative URL support

If you are mounting GitLab at a relative URL, e.g.
`example.com/gitlab`, then you should also use this relative URL in
the `authBackend` setting:

```
gitlab-workhorse -authBackend http://localhost:8080/gitlab
```

## Installation

To install gitlab-workhorse you need [Go 1.5 or
newer](https://golang.org/dl) and [GNU
Make](https://www.gnu.org/software/make/).

To install into `/usr/local/bin` run `make install`.

```
make install
```

To install into `/foo/bin` set the PREFIX variable.

```
make install PREFIX=/foo
```

On some operating systems, such as FreeBSD, you may have to use
`gmake` instead of `make`.

## Error tracking

GitLab-Workhorse supports remote error tracking with
[Sentry](https://sentry.io). To enable this feature set the
GITLAB_WORKHORSE_SENTRY_DSN environment variable.

Omnibus (`/etc/gitlab/gitlab.rb`):

```
gitlab_workhorse['env'] = {'GITLAB_WORKHORSE_SENTRY_DSN' => 'https://foobar'}
```

Source installations (`/etc/default/gitlab`):

```
export GITLAB_WORKHORSE_SENTRY_DSN='https://foobar'
```

## Tests

Run the tests with:

```
make clean test
```

### Coverage / what to test

Each feature in gitlab-workhorse should have an integration test that
verifies that the feature 'kicks in' on the right requests and leaves
other requests unaffected. It is better to also have package-level tests
for specific behavior but the high-level integration tests should have
the first priority during development.

It is OK if a feature is only covered by integration tests.

## License

This code is distributed under the MIT license, see the LICENSE file.

[brief-history-blog]: https://about.gitlab.com/2016/04/12/a-brief-history-of-gitlab-workhorse/
