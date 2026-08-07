# Third-Party Notices

Sharko is licensed under Apache-2.0 (see [LICENSE](LICENSE)). This file lists
third-party content bundled in this repository under a different license.

## BMAD-METHOD skills (`.claude/skills/bmad-*`)

The `bmad-*` folders under `.claude/skills/` are AI agent skill definitions
from the [BMAD-METHOD](https://github.com/bmad-code-org/BMAD-METHOD) project.

- **Copyright:** BMad Code, LLC
- **License:** MIT

These files are plain-text instructions (Markdown, YAML, and small helper
scripts) that tell an AI coding agent how to run a planning or review
workflow — they contain no Sharko code and no Sharko-specific configuration.
They are committed here so that anyone cloning this repository has the same
AI-assisted workflow the maintainer uses, without a separate install step for
the skill content itself.

**Not included:** the BMAD-METHOD *engine* (`_bmad/`) that executes these
skills is not distributed in this repository. It's a separate install — see
[CONTRIBUTING.md](CONTRIBUTING.md) for the one-line setup. If it isn't
installed, or a `bmad-*` skill otherwise isn't available in your session, the
role files under `.claude/team/` are the fallback (see
[CLAUDE.md](CLAUDE.md)).

### MIT License text

```
MIT License

Copyright (c) BMad Code, LLC

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
