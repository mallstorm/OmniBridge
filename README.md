# OmniBridge

Private release archive for **OmniBridge — Universal AI Gateway**.

## Source archives

- `archives/OmniBridge_1.0.0-rc4_SOURCE.zip` — latest complete baseline recovered from the project history; stored byte-for-byte without modification.
- `archives/OmniBridge_1.0.0-rc5_SOURCE.zip` — Yandex AI Studio native provider and heavy-load stabilization candidate.

Original RC4 SHA-256:

`da6f35c3adba1b12e59cace307f6991836e4c4e345ce79e49eb48b460c830623`

RC5 source candidate SHA-256:

`6c4062471e3b95bff710e5a2ff639eeb412f3ab9999e0edc464bfb01ded776d0`

The development history, original product specification summary, Yandex branch context and RC5 validation boundary are documented in `docs/PROJECT_HISTORY_AND_YANDEX_CONTEXT_RU.md` inside RC5 and at the repository root.

## RC5 highlights

- native Yandex AI Studio executor using `Authorization: Api-Key` and `OpenAI-Project`;
- official Models, Responses and Chat Completions endpoints;
- API key remains in OmniBridge Secret Store;
- cached-input telemetry for Chat and Responses;
- bounded generation, Doctor and discovery concurrency;
- normalized `429 gateway_busy`, panic isolation and coalesced statistics persistence.

GitHub Actions extracts the RC5 archive on Windows, runs Go tests/vet, validates UI, and builds the Windows x64 release artifact.
