# bucketmap

[![ci](https://github.com/FCAgreatgoals/bucketmap/actions/workflows/ci.yml/badge.svg)](https://github.com/FCAgreatgoals/bucketmap/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/FCAgreatgoals/bucketmap.svg)](https://pkg.go.dev/github.com/FCAgreatgoals/bucketmap/routes)
[![License: AGPL-3.0 with linking exception](https://img.shields.io/badge/license-AGPL--3.0%20%2B%20linking%20exception-blue)](LINKING_EXCEPTION.md)

**Know how Discord rate limits every REST route, and check that your proxy
does the same.**

bucketmap is two things:

- **An index** of the 314 Discord REST routes a bot can reach, each with what
  is known of its rate limit: which parameter splits its counters, whether it
  counts toward the global limit, how its bucket refills, which routes share it.
- **An engine** that walks a test guild through those routes with a few
  requests each, and reports what every route answered and which bucket it
  fell in. Run it against Discord, then against your proxy, and compare.

Useful if you write a Discord library, run a REST proxy in front of Discord,
build anything that stands in for Discord, or just want to know how a route is
limited.

## Contents

- [Quick start](#quick-start)
- [What a run gives you](#what-a-run-gives-you)
- [Testing a proxy](#testing-a-proxy)
- [Running part of the scenario](#running-part-of-the-scenario)
- [Using the index](#using-the-index)
- [Two ways a bucket refills](#two-ways-a-bucket-refills)
- [What the engine touches](#what-the-engine-touches)
- [In CI](#in-ci)
- [Keeping the index current](#keeping-the-index-current)
- [License](#license)

## Quick start

**1. Prepare a test guild.** Create a guild and put `bucketmap` in its name: the
engine refuses to touch any guild without it. Invite your bot as an
administrator, and four accounts to act as test users.

**2. Install.**

```sh
go install github.com/FCAgreatgoals/bucketmap/cmd/bucketmap@latest
```

**3. Run.**

```sh
export TOKEN=your-bot-token
bucketmap run -guild GUILD_ID -users USER1,USER2,USER3,USER4
```

The run takes ten minutes, prints a summary, and writes the full report to
`bucketmap.json`. Everything it creates is deleted at the end.

## What a run gives you

- **Every request, and its answer:** status, latency, and the rate limit
  headers Discord sent.
- **Every bucket met:** its limit, how long it takes to refill, and whether it
  refills one request at a time or all at once. Buckets several routes share
  are flagged.
- **Every failure:** a step that did not answer as expected, and every 429,
  which should never happen, since the engine never pushes a bucket to its end.

The numbers are your bot's own: Discord's limits differ from one application
to another, and change over time.

## Testing a proxy

Run once against Discord, once against the proxy, and compare:

```sh
bucketmap run -guild GUILD_ID -users USER1,USER2,USER3,USER4 -report direct.json
bucketmap run -api http://localhost:8080/api/v10 -guild GUILD_ID -users USER1,USER2,USER3,USER4 -report proxied.json
bucketmap compare direct.json proxied.json
```

The proxy conforms when every step answered the same and nothing through it hit
a 429. The same works for anything that stands in for Discord.

## Running part of the scenario

The scenario is split into groups: `messages`, `reactions`, `pins`,
`threads`, `attachments`, `polls`, `channel`, `invites`, `webhooks`, `guild`,
`guild-reads`, `roles`, `members`, `automod`, `emojis`, `stickers`,
`soundboard`, `events`, `templates`, `community`, `application`, `commands`,
`users`, `public`, `moderation`. The preflight, the setup and the cleanup
always run.

```sh
# Just some groups, and those they need
bucketmap run -guild GUILD_ID -users ... -only community,events -report more.json

# Only the groups an earlier run left something to learn in
bucketmap run -guild GUILD_ID -users ... -missing bucketmap.json -report more.json

# One report out of several runs
bucketmap merge -report all.json bucketmap.json more.json
```

`-missing` picks the groups with a route no report has seen, or whose bucket
model is still unknown. After turning a guild into a community guild, it
runs the community steps and nothing else you already have.

`bucketmap learn all.json` records the models a run settled in
[`routes/annotations.json`](routes/annotations.json), the model only, and
`bucketmap index` puts them in the index.

## Using the index

The index is [`routes/index.json`](routes/index.json), plain JSON any language
can read. An entry looks like this:

```json
{
  "method": "PATCH",
  "path": "/channels/{channel_id}",
  "name": "Update channel",
  "source": "discord",
  "auth": ["bot"],
  "major": "channel_id",
  "global": true,
  "model": "unknown",
  "coverage": "exercised",
  "notes": ["Changing a channel's name or topic sits behind a sub-limit of its own, ..."]
}
```

| Field | Meaning |
|---|---|
| `major` | The parameter that gives each value its own counter: `channel_id`, `guild_id`, `webhook_id`, or `webhook_id+webhook_token`. Empty when every call shares one counter. |
| `global` | `false` for routes exempt from the bot's global limit. |
| `model` | `token_bucket`, `fixed_window`, `global_only` for a route Discord does not limit on its own, or `unknown` until a run has settled it. |
| `family` | Routes Discord counts together, in a single bucket. |
| `source` | `discord` for [Discord's OpenAPI specification](https://github.com/discord/discord-api-spec), `userdoccers` for routes only [Discord Userdoccers](https://docs.discord.food) documents, such as `POST /guilds/{guild_id}/members-search`. |
| `coverage` | Whether the engine exercises the route, and if not, why (in `notes`). |
| `notes` | Sub-limits and other special cases. |

From Go, match a request to its route:

```go
import "github.com/FCAgreatgoals/bucketmap/routes"

r, ok := routes.Match("PATCH", "/api/v10/channels/123/messages/456")
// r.Path             "/channels/{channel_id}/messages/{message_id}"
// r.MajorValue(path) "123"
```

## Two ways a bucket refills

`X-RateLimit-Reset-After` is the time until the bucket is **full** again, and
that means two different things depending on the bucket:

| | Fixed window | Token bucket |
|---|---|---|
| Refills | All at once, when the window ends | One request at a time |
| `now + Reset-After` from one request to the next | Stays put | Moves forward by one request's worth |
| `Reset-After` as the bucket drains | Shrinks | Grows |

Two requests on the same bucket, back to back, are enough to tell them apart,
far below any limit. The engine sends every read twice in a row, and a few
writes that undo themselves, on purpose.

Some routes are not limited on their own at all: Discord answers them with a
limit of a thousand that resets within a millisecond, and only the global
limit holds them back. The index marks them `global_only`.

## What the engine touches

It works only in the guild whose name carries the marker, spreads about 370
requests over the run (`-duration`, ten minutes by default), waits out any
bucket that reports nothing left, and never retries a 429. Every request
carries an audit log reason naming its step, and everything it creates is
named `bucketmap` and deleted at the end.

What cannot be undone within a run only happens when you ask for it:

| Flag | Does |
|---|---|
| `-allow-kick` | Kicks test user 3. |
| `-allow-ban` | Bans then unbans test user 4, one by one and in bulk. |
| `-allow-prune` | Prunes members inactive for thirty days who hold no role. |
| `-allow-global-commands` | Creates then deletes a global command, seen for a moment in every guild of the bot. |

Kicked and banned users rejoin through the invite printed at the end. Steps
that need a community guild (announcement and stage channels, welcome screen,
membership screening) run when the guild has the `COMMUNITY` feature.

`bucketmap coverage` shows what is exercised: 179 routes on every run, 24 more
under a flag or on a community guild. The rest need an interaction started by a
user, an OAuth2 token, or would change something a run cannot restore, and
each says why in its notes.

## In CI

Call the [`conformance`](.github/workflows/conformance.yml) workflow from your
own repository. It runs the engine and uploads the report:

```yaml
jobs:
  bucketmap:
    uses: FCAgreatgoals/bucketmap/.github/workflows/conformance.yml@main
    with:
      api: https://discord.com/api/v10   # or your proxy
    secrets:
      token: ${{ secrets.BUCKETMAP_TOKEN }}
      guild: ${{ secrets.BUCKETMAP_GUILD }}
      users: ${{ secrets.BUCKETMAP_USERS }}   # four ids, comma separated
```

## Keeping the index current

The [`index`](.github/workflows/index.yml) workflow rebuilds the index every
week from the latest sources, and fails when they describe a route the index
does not know yet. To rebuild it by hand:

```sh
git clone --depth 1 https://github.com/discord/discord-api-spec /tmp/spec
git clone --depth 1 https://github.com/discord-userdoccers/discord-userdoccers /tmp/userdoccers
bucketmap index -spec /tmp/spec/specs/openapi.json -userdoccers /tmp/userdoccers/pages
```

What the sources do not say lives in
[`routes/annotations.json`](routes/annotations.json): bucket models, families,
notes, and why a route is left out. The build fails on any route that is
neither exercised nor explained.

## License

AGPL-3.0, with a [linking exception](LINKING_EXCEPTION.md): using bucketmap as
a library, or running it against your own API, proxy or bot, imposes nothing
on your code. Modifications to bucketmap itself, used publicly, must be
published under the AGPL-3.0.
