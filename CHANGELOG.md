# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-07-31

### Added

- `goatc`, a YAML-driven Agent compiler that embeds local Go plugins in a Bubble Tea executable and supports provider-based loading of multiple gRPC tool services and MCP servers over stdio, SSE, or Streamable HTTP.
- Tagged releases publish versioned `goatc` archives for Linux, macOS, Windows, and FreeBSD on `amd64` and `arm64`, plus SHA-256 checksums.

### Changed

- React Agent skill roots are configurable per `Do` call through `AgentDoArgs.SkillsDir` and propagated to skill tools and callbacks through `AgentContext` metadata.

## [0.1.0] - 2026-07-29

### Added

- Asynchronous native tool-calling agent runtime with live steering, context compression, multimodal messages, callbacks, webhooks, and typed step streams.
- Persistent conversation context backed by RAM, files, SQLite, or MySQL.
- Built-in planning, skills, terminal, and shell tools, plus MCP, Go shared-library, and gRPC tool integrations.
- OpenAI-compatible, Gemini, Cohere, Voyage AI, and Ollama embedding clients.
- Dense vector, BM25, and hybrid Milvus retrievers, along with reusable prompt and streaming packages.
- Examples and package documentation for the agent, embedding, retrieval, prompt, and streaming APIs.
- GitHub Actions checks for formatting, vetting, race-enabled tests, coverage, vulnerability scanning, and CodeQL analysis.
- Contribution, security, code of conduct, and GitHub issue and pull request guidance.
- Dependabot configuration for Go modules and GitHub Actions.

[Unreleased]: https://github.com/torrischen/goat/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/torrischen/goat/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/torrischen/goat/releases/tag/v0.1.0
