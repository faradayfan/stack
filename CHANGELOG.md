# Changelog

## [0.1.2](https://github.com/faradayfan/stack/compare/v0.1.1...v0.1.2) (2026-06-25)


### Features

* **cli:** notify when a newer stack release is available ([6810ee2](https://github.com/faradayfan/stack/commit/6810ee2e615932a1141bd566c9cb93f26a57945c))

## [0.1.1](https://github.com/faradayfan/stack/compare/v0.1.0...v0.1.1) (2026-06-25)


### Features

* **cli:** add `stack update` self-updater ([eb69541](https://github.com/faradayfan/stack/commit/eb695415abdc71bb2529c86173039001e7fe1972))

## 0.1.0 (2026-06-25)


### Features

* **cli:** add `stack version` + resolve version from build info ([3d0453f](https://github.com/faradayfan/stack/commit/3d0453f8f2fd4e8abc81a2acd5ad50fb07662c88))
* **cli:** make env files optional — a pattern is runnable directly ([520629f](https://github.com/faradayfan/stack/commit/520629fcc54c51e26402d437b3b389cd28597d92))
* **engine:** declarative pipeline — stage order is data, gating by list position ([3316853](https://github.com/faradayfan/stack/commit/3316853f96912b124d530b5d30bfe0843bceb062))
* M1 — stack CLI drives the k8s pattern end-to-end ([27d8dfa](https://github.com/faradayfan/stack/commit/27d8dfa4989575552a424d63dfbb5571e7615b3f))
* M2 — the `check` flow (verification as the single CI definition) ([d86aac0](https://github.com/faradayfan/stack/commit/d86aac0a8c62ec5a9ec4e616846898ba53c6ebd6))
* **setup:** cover all pattern tools, not just check tools ([cef1e78](https://github.com/faradayfan/stack/commit/cef1e787c1631663b002039d5cced2f97c31eea5))
* tools manager + `stack setup` (asdf-first, version-verified) ([8517119](https://github.com/faradayfan/stack/commit/85171196f93b8eb8bc7700c0c09d09f616c941b0))


### Bug Fixes

* **engine:** preserve newlines in multi-line command templates ([78016b4](https://github.com/faradayfan/stack/commit/78016b4fb1c5c8c7440131f237575fb0ccd75407))
* **engine:** resolve all tokens in apply `set` values, not just now_unix ([3445451](https://github.com/faradayfan/stack/commit/3445451420113484b5d6038bc6d334400f26a7fa))
* fixed check issues ([bfd6dd4](https://github.com/faradayfan/stack/commit/bfd6dd477f7be05003867229e88d00f4132eb7b0))
* **manifests:** quiet gosec and govulncheck check output ([1b9dbac](https://github.com/faradayfan/stack/commit/1b9dbac777e208e4a1f81b60411ebe050e1c1b30))


### Miscellaneous Chores

* release initial version as 0.1.0 ([e60e92a](https://github.com/faradayfan/stack/commit/e60e92ab5790b6517f689d4a06b857d42a50e9bc))
