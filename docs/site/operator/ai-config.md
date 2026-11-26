# AI configuration

Sharko can use an AI model for two things:

- **Upgrade analysis.** It sums up what changes in a chart upgrade, points
  out what could go wrong, and lists what you need to do.
- **Notes on values files.** When Sharko writes a values file for a new
  addon, the AI can add a short `# description` comment to each field, and
  help find the fields that differ from cluster to cluster. This runs on
  top of the normal [smart values](../user-guide/smart-values.md) steps.
  Those steps run whether or not AI is set up.

Both use the same AI provider. This page is for the person setting up the
provider and keeping an eye on cost. The Helm values for AI are listed in
[Configuration → AI Provider](configuration.md#ai-provider).

## Setting up a provider

**Settings → AI** lists the providers you can pick:

| Provider | What you enter | Notes |
| --- | --- | --- |
| Gemini (Google) | API key (starts with `AIza`) | The first one in the list. The UI suggests `gemini-2.5-flash` |
| Claude (Anthropic) | API key (starts with `sk-ant-`) | The UI suggests `claude-sonnet-4-20250514` |
| OpenAI | API key (starts with `sk-`) | The UI suggests `gpt-4o` |
| Ollama (local) | Address only | No API key. Runs in the cluster or next to Sharko. The UI suggests `llama3.2` |
| Custom OpenAI-compatible (`custom-openai`) | API key and base URL | For a gateway you run yourself |

No provider is picked until you pick one. The Helm default for
`ai.provider` is empty.

Sharko saves these settings in a Kubernetes Secret called
`sharko-ai-config`, in the namespace Sharko runs in. They are encrypted with
the same key Sharko uses for connection tokens, which is
`SHARKO_ENCRYPTION_KEY`. Saving AI settings is written to Activity History
as `ai_config_updated`.

Use the **Test** button in Settings → AI to check that Sharko can reach the
provider with the saved settings. **Test AI** in the form checks the
settings you are typing before you save them.

Both tests are for admins only (`POST /api/v1/ai/test` and
`POST /api/v1/ai/test-config`). Activity History records them as
`ai_tested` ("tested the AI provider") and `ai_config_tested` ("tested AI
settings before saving"). A message to the assistant is recorded as
`ai_chat_message` ("sent a message to the AI assistant"), and
`ai_config_updated` carries `provider=NAME` in its detail.

When you edit a saved provider, the form says "A key is already saved. Leave
this empty to keep it." A key, model or address that you leave out is kept as
long as the provider stays the same. A different provider gets nothing
carried over. To remove a saved key, turn AI off with **Disable**. After
**Save** or **Disable** the assistant buttons appear or go away without a
page reload.

## Which addresses Sharko will call

Only an admin can make Sharko call an AI address.

- A self-hosted address in a private network or on localhost is allowed. A
  self-hosted Ollama lives there.
- Addresses starting with `169.254` or `fe80:` are never called.
- An address must start with `http://` or `https://`.
- An address may not hold a user name or password.

What Sharko says when an address is refused:

- "The address must start with http:// or https://."
- "Take the user name and password out of the address. If the provider needs a key, put it in the API key field."
- "Sharko does not call this address. Addresses that start with 169.254 (and fe80:) belong to the machine's own network setup, and no AI provider lives there."
- "Enter the address of the provider first."

What it says about a key before it calls anything:

- "Enter an API key first."
- "The API key has a space or a line break in it. Paste the key again, on one line."
- "The API key is too long to be a real key. Paste the key again."

## What a provider error looks like

Sharko never passes a provider's answer on as it came. It writes its own
sentence, and a key is never shown in it, not even masked. The shape is:

```text
openai returned 401: the provider refused the API key. The provider said: "..."
```

| Provider answer | Sharko says |
| --- | --- |
| 401 or 403 | "the provider refused the API key." (Ollama: "the server refused the request.") |
| 404 | "the provider did not find the model or the address." (Ollama: "this address does not answer like an Ollama server, or the model is not there.") |
| 429 | "the provider's request limit or spending limit was reached." |
| 500 and above | "the provider had a problem of its own." |
| Anything else | "the provider did not accept the request." |

The assistant cannot read values files on the current repo layout. It says:
"The assistant cannot read values files in this version. They are in your Git
repo: values/global/<add-on>.yaml for every cluster, or
values/clusters/<cluster>/<add-on>.yaml for one cluster."

## The "Annotate values on generate" switch

This switch in Settings → AI decides whether the AI adds notes when you
add an addon. It is on by default once an AI provider is saved. With no provider (or after you click **Disable**), the switch shows off, because there is nothing to write the notes with.
Turning it off stops the notes for every addon. You can also turn them off
for one addon only (see [below](#turning-the-notes-off)).

The first lines of each values file say which way it was made:
`# AI annotation: enabled` or `# AI annotation: disabled`. You can search
your addons repository for these lines to find the files that have no AI
notes.

## Cost

Each time the notes run, Sharko sends the chart's own `values.yaml` to the
AI provider and gets back about the same amount of text. Most chart values
files are 2 to 15 KB. That is a small request, so the cost per addon is
very low on every provider. Check your provider's price list for real
numbers.

It runs once when you add an addon. So the total cost depends on how many
addons you add.

If a chart's `values.yaml` is bigger than 50,000 bytes, Sharko does not
call the AI for it at all. You get the file without AI notes. This stops
one very large chart from using up your budget.

## Time limit

Sharko waits at most 30 seconds for the AI to answer. If there is no answer
by then, Sharko stops waiting and writes the file without AI notes. This
way a slow provider never holds up adding an addon.

If you use Ollama, the 30 seconds is the same whatever model you run. A big
local model may run out of time again and again. Watch the
`sharko_ai_annotate_total{outcome="timeout"}` counter, and switch to a
faster model if more than about 1 in 10 calls time out.

## Secrets are never sent to the AI

Before any part of a values file leaves Sharko, Sharko scans it for
anything that looks like a secret. It looks for AWS keys, GitHub tokens,
JWTs, Google API keys, Slack tokens, PEM private keys, and lines like
`apiKey: <16 or more characters>`, `password: <16 or more characters>` or
`token: <16 or more characters>`.

If it finds anything, the AI call is **refused**. There is no way to send
it anyway. You get the file without AI notes, and Activity History gets two
entries:

- `ai_annotate_blocked`, with a detail like `chart=demo version=1.0 matches=3`
- `secret_leak_blocked`, so you can search for every blocked leak in one
  place

The secret values themselves never show up in Activity History, in logs or
in API answers. They are replaced with `***`.

If a chart really ships secret values in its `values.yaml`, the fix is in
the chart: move the secret to a SealedSecret or an ExternalSecret. Sharko
will not make the leak worse by sending it to an AI provider.

## Metrics

Prometheus metrics for the AI notes:

```
# Calls, counted by how they ended. These eight are every value the
# running server writes.
sharko_ai_annotate_total{outcome="ok"}
sharko_ai_annotate_total{outcome="not_configured"}
sharko_ai_annotate_total{outcome="empty_input"}
sharko_ai_annotate_total{outcome="oversize"}
sharko_ai_annotate_total{outcome="secret_blocked"}
sharko_ai_annotate_total{outcome="timeout"}
sharko_ai_annotate_total{outcome="llm_error"}
sharko_ai_annotate_total{outcome="parse_error"}

# How long calls take, split by how they ended.
sharko_ai_annotate_latency_seconds_bucket{outcome="ok"}
sharko_ai_annotate_latency_seconds_count{outcome="ok"}
sharko_ai_annotate_latency_seconds_sum{outcome="ok"}
```

The metric's own help text also names `opted_out` and `disabled`. Nothing in
Sharko ever writes either one, so do not write an alert that expects them.
If you see them in the help text and not in your data, the help text is out
of date. Your scrape is fine.

### Alerts worth having

- **Many `secret_blocked`** (more than 5 an hour): a chart in your catalog
  ships values that look like secrets. Look at recent addon adds and fix
  the chart.
- **Many `timeout`** (more than 1 in 10 calls): your AI provider is slow or
  having trouble. Switch providers, or turn the notes off for now.
- **A jump in `llm_error`**: usually a problem with the API key, for
  example it was changed or you ran out of quota. Press **Test** in
  Settings → AI.

## Turning the notes off

To turn the AI notes off for every addon but keep the AI set up (upgrade
analysis still works), turn off **Annotate values on generate** in
Settings → AI. Activity History records `ai_config_updated`.

To turn the notes off for one addon, use the switch for it on the addon's
Catalog tab. Sharko writes a `# sharko: ai-annotate=off` line at the top of
that addon's values file. It stays there when the chart is upgraded.

## Sharko keeps no copy of AI answers

Sharko does not keep AI answers anywhere of its own. The values file in your
Git repository is the only copy. Once an addon is added, its file lives at
`values/global/<addon>.yaml` (in a repository with the older v3 layout, it
is under `configuration/`), and Sharko reads it from Git after that. The AI
is only called when you add an addon.
