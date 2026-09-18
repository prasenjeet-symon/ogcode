# Contributing to Ogcode

Contributions are welcome — bug fixes, features, and documentation alike.

## Workflow

1. **Fork** the repository.
2. Create a feature branch: `git checkout -b feature/my-improvement`.
3. Make your changes and add tests where applicable.
4. Run `go test ./...` and make sure it passes.
5. Submit a pull request.

Please follow the existing Go style — match the surrounding code rather than introducing
a new convention.

## License of your contribution

Ogcode is [dual-licensed](LICENSING.md): AGPL-3.0-only for everyone, plus a commercial
license for organizations that cannot comply with the AGPL. That second track only works
if the project is entitled to relicense every line it ships. A single contribution that
is AGPL-only would make it impossible to offer a commercial license covering the file it
touched.

So, **by submitting a pull request you agree to the following.**

1. **You keep your copyright.** Nothing here assigns it away. You may use your own
   contribution however you like, elsewhere, forever.

2. **You grant a license to the maintainer.** You grant Prasenjeet Symon a perpetual,
   worldwide, non-exclusive, irrevocable, royalty-free right to reproduce, modify,
   publicly display, sublicense, and distribute your contribution — as part of Ogcode or
   any derivative of it — under the AGPL-3.0, under the Ogcode Commercial License, and
   under any other license terms the maintainer applies to Ogcode.

3. **You grant a patent license** on the same terms, covering any patent claims you own
   that your contribution would necessarily infringe.

4. **You confirm you are entitled to do this** — the work is yours, or you have the
   rights to submit it; it isn't covered by an employer agreement that would prevent the
   grant; and any third-party code you include is identified, with its license, in the
   pull request.

5. **No warranty.** Your contribution is provided as-is, without warranty of any kind.

If you can't agree to this — for example, your employer owns your output — say so in the
pull request. Small fixes can often still be taken; larger ones may need a signature from
whoever holds the rights.

## Sign-off

Add a `Signed-off-by` line to each commit, certifying the
[Developer Certificate of Origin](https://developercertificate.org/):

```bash
git commit -s -m "your message"
```

## Third-party dependencies

Ogcode ships under the AGPL and offers a commercial license, so new dependencies have to
be compatible with both. Before adding one, check its license:

- **Fine:** MIT, BSD-2/3-Clause, Apache-2.0, ISC, MPL-2.0, Unlicense.
- **Ask first:** GPL, AGPL, SSPL, LGPL, and any source-available or "non-commercial"
  license. These are compatible with the AGPL track but not with the commercial one, and
  usually rule the dependency out.

Note the license of anything new in your pull request description.
