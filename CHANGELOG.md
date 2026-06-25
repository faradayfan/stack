# Changelog

## 1.0.0 (2026-06-25)


### ⚠ BREAKING CHANGES

* **engine:** `type:` is removed from patterns; declare a `pipeline:` instead.
* **config:** the .stack app/env schema changed; no back-compat.

### Features

* **cli:** make env files optional — a pattern is runnable directly ([64ff7be](https://github.com/faradayfan/stack/commit/64ff7be322cd19be0e82eca9f469e8fb4add47f2))
* **engine:** declarative pipeline — stage order is data, gating by list position ([ea4c110](https://github.com/faradayfan/stack/commit/ea4c110921a30e0e48d32b9607b8ec4d28473cbc))
* M1 — stack CLI drives the k8s pattern end-to-end ([e911295](https://github.com/faradayfan/stack/commit/e911295bc4ae35cc6fcdc01d3329954d2017b3f3))
* M2 — the `check` flow (verification as the single CI definition) ([dcf7720](https://github.com/faradayfan/stack/commit/dcf7720d8a30d2ba0269dfee6f7780f3875d32fd))
* **setup:** cover all pattern tools, not just check tools ([87e878a](https://github.com/faradayfan/stack/commit/87e878acad8782750c10b3029e86e8b8cd262130))
* tools manager + `stack setup` (asdf-first, version-verified) ([87066a3](https://github.com/faradayfan/stack/commit/87066a3b377d4a0f8727437d57a8cffcf8531d32))


### Bug Fixes

* **engine:** preserve newlines in multi-line command templates ([4485e8b](https://github.com/faradayfan/stack/commit/4485e8be008b1e90a449abd6670a2c008c2bc4cf))
* **engine:** resolve all tokens in apply `set` values, not just now_unix ([242eb8f](https://github.com/faradayfan/stack/commit/242eb8f70bb0c1ecb10c062d56b438a091d32876))
* fixed check issues ([b7d6901](https://github.com/faradayfan/stack/commit/b7d6901387d9bf93d2a5d4efe5cbeb6e4db65957))
* **manifests:** quiet gosec and govulncheck check output ([7b6cc65](https://github.com/faradayfan/stack/commit/7b6cc65c2202ec56b321e27fdc49476cc92ed7be))


### Code Refactoring

* **config:** schema v2 — pattern templates + uniform merge ([46d9807](https://github.com/faradayfan/stack/commit/46d980705fd6417ffdea0f914774e4a3efaf17dd))
* **engine:** drop pattern `type` — stages run their step block's tool ([22684f4](https://github.com/faradayfan/stack/commit/22684f42860b1c95365fb87a07bae65b16b67487))
