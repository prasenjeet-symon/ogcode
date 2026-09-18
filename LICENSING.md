# Licensing

Ogcode is **dual-licensed**. You choose the track that fits you:

| | **GNU AGPL v3.0 (only)** | **Ogcode Commercial License** |
|---|---|---|
| Cost | Free | Paid |
| Source obligations | Yes — see below | None |
| Who it's for | Individuals, teams, companies running Ogcode for their own work | Vendors embedding Ogcode, hosted-service operators, and companies whose policy forbids AGPL |
| Text | [LICENSE](LICENSE) | Issued on signature — contact us |

Copyright © 2026 Prasenjeet Symon. All rights reserved.

---

## Using Ogcode under the AGPL

Most people owe nothing and need to do nothing. The obligations attach when you
*give Ogcode to someone else* — by shipping it, or by putting it in front of users
over a network.

| What you're doing | AGPL | Commercial license needed? |
|---|---|---|
| Running Ogcode on your own machine, for your own work — at home or at a company of any size | Fine. No obligations. | No |
| Modifying Ogcode and keeping the changes to yourself | Fine. Private modifications are never triggered by the AGPL. | No |
| Running a **modified** Ogcode that other people interact with over a network (including your own colleagues via the web UI, console, or control plane) | Allowed — but §13 applies: those users must be offered the Complete Corresponding Source of the exact version you are running. | No, if you publish the source |
| Offering Ogcode — modified or not — as a **hosted, managed, or SaaS product** to third parties | Allowed — but you must release the complete source of everything you run, under AGPL-3.0. That includes the agent, the control plane, the worker, the web console, and every change you made. | Yes, if you don't want to publish |
| **Embedding** Ogcode in a closed-source product you distribute | Not permitted. The AGPL's terms would extend to your product. | **Yes** |
| Removing or working around the AGPL's source obligations | Not permitted. | **Yes** |

### §13, in plain terms

Section 13 is the clause that separates the AGPL from the ordinary GPL, and it is the
reason Ogcode uses it. If you let *other people* use Ogcode across a network, they are
entitled to the source of exactly what you are running — not the upstream release, *your*
build, with your modifications, offered from the interface they use.

A competing hosted product built on Ogcode is therefore possible, but only in the open:
its operator has to hand its own users the source of the whole thing. If that isn't
acceptable to you, buy a commercial license instead — that is the intended path, not a
loophole to be found.

---

## When you need a commercial license

- You want to embed Ogcode in a proprietary product you ship to customers.
- You want to run Ogcode (or a fork) as a hosted or managed service without publishing your modifications.
- Your organization's policy prohibits AGPL-licensed software — a common enterprise rule.
- You need warranty, indemnity, support, or an SLA, none of which the AGPL provides (see §§15–16).

The commercial license removes the source-disclosure obligations and is negotiated per
deal. It does **not** change the AGPL terms for anyone else, and it does not make the
public releases proprietary.

### How to get one

Open a licensing enquiry at <https://github.com/prasenjeet-symon/ogcode/issues>, reach
out on [Discord](https://discord.gg/JQP9t8y2Zv), or email **licensing@ogcode.xyz**.
Include your company, the product or service involved, and roughly how you intend to
deploy Ogcode.

---

## Versions released before the change

Releases **up to and including v0.36.1** were published under the MIT License and remain
MIT-licensed forever. A license change is not retroactive and MIT grants cannot be
revoked — anyone may continue to use, fork, and redistribute those versions under MIT.

**v0.37.0 onward is AGPL-3.0-only** plus the commercial option described above.

---

## Contributions

Ogcode can only offer a commercial license for code it is entitled to relicense. So
contributions carry an inbound license grant — see [CONTRIBUTING.md](CONTRIBUTING.md)
before opening a pull request. In short: you keep the copyright in your contribution and
grant the maintainer the right to license it, including under the commercial license.

---

## Third-party components

Ogcode bundles third-party code under its own terms — see
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

One of them matters to commercial licensees: PDF rendering uses
`github.com/gen2brain/go-fitz`, which vendors **MuPDF** and is itself **AGPL-3.0**.
An Ogcode commercial license covers Ogcode's own code — it cannot and does not grant
rights to MuPDF. If you take a commercial license and need PDF rendering, obtain a MuPDF
license from [Artifex](https://artifex.com/licensing/), or build Ogcode without that
component.
