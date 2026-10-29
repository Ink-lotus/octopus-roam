# 发布准备工作包 5：publish=false 完整构建、下载与核验

日期：2026-10-25。执行起始：2026-10-24。工作目录：`D:\tool\AI\octopus-roam`。范围：[RC 交接计划](release-rc-handoff-20261021.md) D 的构建、下载和 verify；依据 [发布 CI 合同](../docs/release-ci.md)、[主计划 R3](release-plan-v0.1.0.md)及工作包 [3](release-step-03-result-20261023-01.md)、[4](release-step-04-result-20261024-01.md)的实际结果。

**工作包 5 完成。** 同一冻结候选的 prepare、test、完整 11 目标程序、NDK 四 ABI、四架构镜像及 seal 均成功；下载后的原始合同在 Linux 上完整 verify 通过，退出码 0。publish 因 false 跳过。本会话没有启动产品或数据库；工作包 6 直接消费下面的原始 artifact。

完整机器记录为 [work-package-05-result.json](../build/release-local/step-05-publish-false-20261024-01/evidence/work-package-05-result.json)；工作包 6 的外部审核输入为 [step-06-inputs.json](../build/release-local/step-05-publish-false-20261024-01/evidence/step-06-inputs.json)。

| 固定身份 | 准确值 |
| --- | --- |
| 版本 / 完整 SHA | `v0.1.0-rc.1` / **`5ec4dc5905149db03159d9d02c8f76646300d477`** |
| Tree / SOURCE_DATE_EPOCH | `ddd0aab81a355bbcc96b395178d2de3bb108113c` / `1792752156` |
| 普通 main CI | [39941450556](https://github.com/Ink-lotus/octopus-roam/actions/runs/39941450556) / attempt 1 / success，同一 SHA |
| 材料 run / attempt / artifact | `40088771849` / `1` / `12557912716`（`release-materials`） |
| materials.json SHA-256 | `65640fab073bc98826a2d4e49f9bad2536d8c5fb3ff6a8a6709627d639b6e2b7` |
| 材料到期 UTC | `2026-11-07T11:12:29Z`（北京时间 2026-11-07 19:12:29） |
| release workflow | ID `391836659`，main / 同一 SHA，active |
| release.yaml Git blob / SHA-256 | `9b3e670b18c175061865a3de47e0461d5ac227e4` / `552069ca5965becd8629ad30654622dc21607be4dda19aad26db6ac8e31d2f8d` |
| 原始候选合同 SHA-256 | `070416ce9bdd3b21e4ea6342135fa8dd3315febf440826c24648ea511b28cef7` |

冻结身份、main、普通 CI、材料 run/artifact 与有效期已回读；build 日志确认实际下载的是材料 artifact `12557912716`，不是历史包或合成输入。工作区 450 个冻结文件在核验前后与工作包 3 原字节清单一致。main 的 workflow 内容与候选 Git blob 一致，major action 引用在本次运行实际解析出的 SHA 保存在原始 job 日志。

用户已明确批准 publish=false dispatch；CLI 首次 401 未创建 run，认证处理后由用户终端触发，并提供 run ID `40104578976`。本报告记录其结果，没有再触发第二次构建。原准备命令为一条物理行：

```powershell
gh workflow run release.yaml --repo Ink-lotus/octopus-roam --ref main -f version=v0.1.0-rc.1 -f commit=5ec4dc5905149db03159d9d02c8f76646300d477 -f materials_run_id=40088771849 -f materials_sha256=65640fab073bc98826a2d4e49f9bad2536d8c5fb3ff6a8a6709627d639b6e2b7 -f publish=false
```

影响为执行 prepare/test/build、读取审核材料、构建并上传保留 14 天的 artifact；publish 条件为 false，发布 job 不执行。实际 run 为 [40104578976](https://github.com/Ink-lotus/octopus-roam/actions/runs/40104578976)，attempt **1**，workflow_dispatch / main / 冻结 SHA，created `2026-10-24T14:54:28Z`，最终 success，updated `2026-10-24T15:09:23Z`。

| Job | ID | 结果 | 开始 / 结束 UTC |
| --- | --- | --- | --- |
| prepare | `120903501592` | **success** | `2026-10-24T14:54:32Z` / `2026-10-24T14:54:48Z` |
| test / test | `120903566531` | **success** | `2026-10-24T14:54:50Z` / `2026-10-24T14:56:26Z` |
| build | `120903906826` | **success** | `2026-10-24T14:56:29Z` / `2026-10-24T15:09:22Z` |
| publish | `120906751096` | **skipped** | `2026-10-24T15:09:22Z` / `2026-10-24T15:09:22Z` |

test 使用完整测试入口，含 default/jsoniter、两组 vet、关键 race、前端、发布合同和 workflow 检查；日志确认 **85 项发布测试通过**。build 实际使用 `go version go1.27.3 linux/amd64`、Python 3.14.6、Node 24.18.0、pnpm 11.15.1、NDK r28c；四 Android 程序均记录 CGO_ENABLED=1，其余七目标为 0。日志记录 `package passed`、四镜像导出、`seal passed`，upload 明确为 **513 文件、retention-days: 14**。

三份原始日志及哈希在 [remote-logs-04/result.json](../build/release-local/step-05-publish-false-20261024-01/evidence/remote-logs-04/result.json)：

| 原始日志 | SHA-256 |
| --- | --- |
| prepare（19,579 字节） | `29bee654c188a4f6ee3a27be7342bfba179f1f73b01165fc16b8a797d101c4cf` |
| test / test（72,564 字节） | `8e81671dc10776cefecf9d651b7692bcbf7e2e9c0d6de6e6e93afdf76f3a0cc5` |
| build（138,577 字节） | `0bf392eaaa3d2bf73f5e81e46122955bfa08b3cbdd67f8e12f4d668de6ad219a` |

| 下载与保留 | 实际值 |
| --- | --- |
| Artifact 名称 | `release-v0.1.0-rc.1-5ec4dc5905149db03159d9d02c8f76646300d477` |
| Artifact ID / API 大小 | **`12564820530`** / `2,762,157,277` 字节 |
| Artifact API digest | `sha256:2647e22e11eb831cb7b485eb9a9a53ede8b6c51d6e44925d482e84bf85a42936` |
| 创建 / 到期 UTC | `2026-10-24T15:09:12Z` / **`2026-11-07T15:08:28Z`** |
| 到期北京时间 | **2026-11-07 23:08:28**；workflow 配置保留 14 天 |
| 下载目录 | `D:\tool\AI\octopus-roam\build\release-local\step-05-publish-false-20261024-01\artifact-download-01` |
| 解包文件数 / 总字节 | **513** / **2,762,007,303** |

下载目录直接为 artifact 根目录，包含 bin、archives、docker、docker-context、images、materials、delivery、build.json 和 artifact.json。sealed 清单覆盖除 artifact.json 自身外的 512 个文件；全部 513 个文件的完整 SHA-256 在 [artifact-SHA256SUMS](../build/release-local/step-05-publish-false-20261024-01/evidence/verify-linux-04/artifact-SHA256SUMS)，该清单 SHA-256 为 `5f7b1aedeb73a5084fbe356ba94f02018e3acddd593092359614a269cb7a9ad3`。外层 Actions ZIP 未在此目录保留，外层 digest 引自 API/上传证据；本地通过的是解包实物、材料、归档、镜像与封印清单的完整核验，不把 API digest 写成已独立计算的 ZIP 哈希。

| 本地审核对象 | 完整 SHA-256 |
| --- | --- |
| `artifact.json` | `22d306092e4874f8c2ed6cea35dddaaf9397ea22a44fe76473e9eb5e65afb8aa` |
| `build.json` | `5582167e1d3e943d27c3fa5850fd091509902815687e1ccd0aa5fd7420ca2ac0` |
| `delivery/BUILD.json` | `5582167e1d3e943d27c3fa5850fd091509902815687e1ccd0aa5fd7420ca2ac0` |
| `materials/materials.json` | `65640fab073bc98826a2d4e49f9bad2536d8c5fb3ff6a8a6709627d639b6e2b7` |
| `delivery/MATERIALS.json` | `65640fab073bc98826a2d4e49f9bad2536d8c5fb3ff6a8a6709627d639b6e2b7` |
| `delivery/IMAGE-MANIFEST.json` | `f3eb6479a2f875390cf9bb014b0f4ce0d3c23db56c07c6c46cedcb2a857f624c` |
| `delivery/SHA256SUMS` | `d7f4bbda7a51680bc0eb97338f9d8cd33fd1779225efedfd1778df3a8aa91b56` |
| `archives/SHA256SUMS` | `e062abcfd126b4ad8f22b65a60766b7732a7e506ef82c96e8409633d76ff9a06` |

11 个原程序的 `go version -m` 全文与逐项结果见 [binary-metadata.json](../build/release-local/step-05-publish-false-20261024-01/evidence/verify-linux-04/binary-metadata.json)及同目录 `<target>.version-m.log`。每个实际编译版本均为 **go1.27.3**，GOOS/GOARCH 正确，vcs.revision 为冻结 SHA、vcs.modified=false，build ID 均准确为 `octopus-roam/v0.1.0-rc.1/5ec4dc5905149db03159d9d02c8f76646300d477`；没有运行这些程序来获取版本。归档程序字节与 bin 相同，公开 delivery ZIP 与内部 ZIP 相同；程序模式 **0100755**、其他文件 **0100644**，文件白名单及类型检查通过，Windows 启动脚本也在白名单内。

| 目标 | 原程序 SHA-256 | ZIP SHA-256 |
| --- | --- | --- |
| linux-amd64 | `f0df35ed408d8d6bf9055cb3d0cf1c3ad5ac7225c842df8ed7797aa1f8c0b604` | `ced42314fcde67cf12f7542746b260835b7265b2efd905614aff46d4412129f3` |
| linux-arm64 | `fd5853507a906b8499a902d39ec6909744c92b1324fa31190f8608ab1c4b2806` | `9bdddc2ee3bb81754c52044ca431c9667af1e483b484de83129814491dc5bfd7` |
| linux-arm | `382015d972d7c446febbeb0c692fd70a482f2a6406dc895bb71890e7132f4eda` | `521df0968dde0a49937dc5b9008e3b02143207134d5ae7fc9221268bb78316b2` |
| linux-386 | `f65934b6058972cc42827df7ed10896593e6ecf5e0dd700e6ee5e5dfa3579134` | `e8ce959998d7547300064229b897503c8800acc9a575da99b18f332dbe50bb51` |
| windows-amd64 | `dba7242dafcf992794f1377ef49065fe7b9148e8b598ef23cd5c5bc06632017b` | `19739b687b0dd28a9dabf62d2b184f78c431027d0e4e319d6a10375fe4b4678f` |
| darwin-arm64 | `f37fd8c875e0ac918da78c40f017c35e6067665067769c309e821d2b7613d256` | `23387f41f4e02bee675235164a088350fbe08b6639a4d592d403618642940c00` |
| darwin-amd64 | `5415722378199cea5e3dfa7bac09549cd78c7592292bb7d6565f872021765c99` | `c9f874d412fc9d489c4304ad9124230229a2a47dee35ba8b37fdb8c0427fd41c` |
| android-amd64 | `4db230a45958f117485569c1c8cc7bbcf35d2641e67f387a757951c570040d27` | `467a98e61430c1c516c814f75bcc3f13aa2c4da69aa008ef30a14b548512a288` |
| android-arm64 | `5b192a9a87801075f9994bdc671e8795795f50689814158e7a108a8ed7e5a997` | `a4e165e016d834494ee705e926ddac4ab81a0f223baa4d03e9cce2e70f9c5b62` |
| android-arm | `b6db46acd1305ebdf980a3a42cf722296fba5c3e3c0f59da3bf67132f678c5c7` | `61c7c89874512d7c3922338ff957641871cce2ca214e0a4890f68cebbf5ad190` |
| android-386 | `85b467306f2327f9db255e2d282495cadc79293a0b0bd481c3df7debed5efb25` | `50746fdf7c91e570e9749b2e43cd9dd87f194f5df7b9d3aead28e8ffd5933865` |

四镜像分别核验配置/层 digest、Linux 平台及 ARM v7、版本/SHA 标签、归档标签、实际程序与对应 Linux bin 的字节、许可证/CSV/声明和 entrypoint。实际配置 Cmd 为 `[/entrypoint.sh]`。原始 APK 数据库保存在 [verify-linux-04](../build/release-local/step-05-publish-false-20261024-01/evidence/verify-linux-04/) 的 `apk-installed-<arch>.txt`；每架构 **18 个包**的 P/V/A/o/c/L 与对应 Alpine 源码材料逐项一致。镜像未加载或启动。

| 架构 | Image ID | 镜像 tar SHA-256 | 实际 APK DB SHA-256 |
| --- | --- | --- | --- |
| amd64 | `sha256:c3edfb7b8e426a136f888c94c13350673c4e8fd16344dbe312b677d723d43ad3` | `3c814bd1d8c134d1be6a345edc89711dd5b4563e20eb75119132bd48864c2428` | `a23816d489b2dcb27da00b46e92ce790b82dffa3990df68b280c3e2ec384b969` |
| arm64 | `sha256:960edc076b385f79d77d6619ca375c04b4d2d2a8226e22744e41c1aabc784d1c` | `baeb3c2bc6961a20bdd3c4563969620bd164ea1b80f4587c414293aec9ca84ff` | `b3eca08cff12c142f12d4f505db16574c0f87e1367799d58700a7d3cfcf6de0c` |
| arm | `sha256:88add6b2426c42dc25bc715d151fdf9afb4a6bfdf4c417b05909a421b4bab051` | `2edb316f87be85eae991bd51120c817e4ace8b1acc8432346cea2b02bba2d8f6` | `fece4e9d207b324ee0225b5d6b30de61ab7fa1d6f85786aa720304c649248dac` |
| 386 | `sha256:d3731efdc2836b2c1bdf82e39cbe7e84785fcb947c8bb5d9eb565d5cc27a4c8d` | `ef23a4c8029ae4e04b9f3efa6574eda61c22516bc1957a6b89f8ee75e82b293c` | `2241ca3f4261cd8b6f173a8a10a4d32475ae254f7c87cffe7c24c54cf9c0a84d` |

材料沿用准确审核的 Alpine 3.24.3 基础索引 `alpine@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a`；本次不是复用旧镜像。源码 ZIP 的内部全文件哈希、release-source.json、声明与说明全部通过原合同；追加核对了 **374 个冻结 Git 文件**、**50 个实际前端文件**和 **110 个不同实际依赖来源**（含 replace，h1 与源码 go.sum 一致）。材料生产记录保存在核验目录的 material-production.json。

| 对应材料文件 | SHA-256 |
| --- | --- |
| `INSTALL.md` | `37cfb3082efd4c029de0debc6b2cd49cc0c5338b0f52f54bf67109df79bce588` |
| `REBUILD.md` | `92248ebec2ffde39437f8e0b88f594cb61684f87d18dff76a1d80f8c1a1d3b09` |
| `SOURCE-OFFER.md` | `d30297de5b3da3e7597d1767effcd6c8058886b78e81314510449ed2dc932789` |
| `THIRD-PARTY-NOTICES.md` | `b353a9bc9936a70e544cf750e706370674e0bb8b98987447870cd130b0e9bac7` |
| `THIRD_PARTY_LICENSES.csv` | `4f8e3ea855859526f7b2614d10756c133d33f926e7f869674ed196a5fffac350` |
| `alpine/386.zip` | `6e5900b6aa0fc5369b9edd2ee1e660dcc655fb39207fd2c3ef473c744f03ba64` |
| `alpine/amd64.zip` | `0fd0472d6c2022ab175819a02587914a0868c3521bc1c0d57b3358bfd00ed9d9` |
| `alpine/arm.zip` | `639acd26e377ae774a258dbb7d7fe16950ee97d5bfe6bdfe8fa731aa23ec6084` |
| `alpine/arm64.zip` | `398b60e4cf2e37e81df9c1a7e110037775e623868cbc42bd66d38e311f888e89` |
| `octopus-roam-source-v0.1.0-rc.1.zip` | `e25b26a4f20f086ade4fcec939db7d66219ffb3fe07b48b824cb650793710995` |
| `source-offer.html` | `472150962377ce325c849e6ef13d74e495f082b74676086883638cc570d31c39` |

核验使用候选 Git blob 原字节的 release_contract.py，没有修改合同或 artifact。Windows 工作区脚本为冻结的 CRLF，Git blob 为 LF；二者仅换行不同，原始 LF 副本和各自哈希已记录。最终 Linux 原命令及临时进程环境、工具来源、工具文件和适配器哈希见 [verify-linux-04/result.json](../build/release-local/step-05-publish-false-20261024-01/evidence/verify-linux-04/result.json)。

原合同 verify 于 `2026-10-25T03:51:24.411990+00:00` 开始，`2026-10-25T03:53:16.016353+00:00` 完成，**退出码 0**，日志为 `release_contract: verify passed`；日志 SHA-256 为 `32e2bfd18c91f31e7d12be4bb417ad0754a3465db8753fe43cd654a8228dc62b`。追加证据于 `2026-10-25T03:53:39.942908+00:00` 全部完成。Go Linux 分发 SHA-256 为 `65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40`，Python Linux 分发为 `29dc7f3887a430fe7a0005fee4732b00be1bbed5bf21aa1e43f8d947eb1b9f61`，均使用工作包 4 已有文件并复核。

失败证据继续保留，分别解释而不覆盖：

- 初始 CLI dispatch 为 401，未创建 run；认证/刷新过程见既有 auth-check、auth-refresh-failure、dispatch-failure JSON。用户最终手动 dispatch 的 run 始终是 attempt 1。
- verify-01 因冻结 Go Windows 分发缺少 `go tool buildid` 退出 2。用户明确答复“授权执行”后，从准确 Go 1.27.3 源码编译临时 cmd/buildid；仅拦截这一只读命令，其余 go 调用转发原工具。Windows 分发的 15,657 文件及冻结 450 输入全部核对。Windows buildid SHA-256 为 `5b180dc6234cd9f91a68a7c89267fad63edee5a23c69af1c514813bf319c51af`；最终 Linux buildid 为 `8743757cc76ae089af1637046d34623aacda1f699c08820110a0042312191cff`。
- 临时驱动 result.json 的预检发现 Windows 子进程从父进程 PATH 找到系统 Go 1.27.0，尚未执行合同便停止。随后只调整临时子进程环境，预检确认 1.27.3 后才运行；旧失败记录保留。
- verify-03 已核过材料、11 程序、四镜像及封印，最后报 `Published checksums differ`。原因是 WindowsPath 对大小写排序的语义与 Linux PosixPath 不同；26 个交付文件的实际哈希全部匹配 Linux 清单，见 checksum-order-diagnosis.json。没有重排或改写产物、没有跳过该检查；转到已有 Linux 工具运行同一原始合同后完整通过。
- remote-readback-02 的受限凭据上下文出现日志 403 / 包查询 401；03 的认证 GET 成功但原始日志含控制字符，CLI 拒绝输出。04 将日志字节直接保存到文件，另保存去控制字符的可读副本，三份完整日志成功；前两轮 result.json 保留为失败，不冒充通过。

本次 remote-readback-03 认证快照确认候选 tag / Release 精确 GET 均为 404，tag 列表为空、公开 Release 为零；工作包 4 的材料 inbox 仍是 draft（ID 422672701），没有将其误记为本次公开 Release。publish job 的步骤为空，只有该 job 声明 contents/packages write；其跳过及 build 的无推送路径证明**本次 run 未创建 tag、Release 或版本镜像，未更新 latest**。GHCR 精确包及版本 API 也返回 Package not found / 404；当前 OAuth scopes 缺 read:packages，因此这些 404 仅记录可见性观察，不独立声称已经证明所有私有包全局不存在。没有为此再次刷新认证。

工作包 6 固定消费本次 `artifact-download-01`，以 step-06-inputs.json 中的 version、commit、artifact_sha256、materials_sha256 四项作为外部审核身份；artifact_sha256 指 **artifact.json**，不能用 API 外层 ZIP digest 替代。原 artifact 须持续可访问，不混用工作包 4 的源码复建程序或历史 acceptance 目录。先按 [验收布局说明](../docs/release-materials.md#4-从-sealed-artifact-准备验收目录)准备新的 derived-acceptance-layout，保留原程序/ZIP 字节及原始 artifact 绑定；后续 TLS、C1/C2、Windows 最终包和容器产品验收另在工作包 6 执行。

工作包 6 的完整合同核验继续使用本次已通过的 Linux Go/Python 与 `evidence/verify-linux-04/bin/go` 临时适配器。原生 Windows verify 仍有 buildid 布局及路径排序兼容限制，不能忽略失败或手动修改 SHA256SUMS；若选择修改产品合同/验收入口或构建工具链，按相应冻结步骤重新审查，而不是把改动塞进本次候选。

本轮使用 PowerShell 7.6.7 调用本地工具与 WSL Linux 核验；认证 GET 使用目标限定的 GitHub CLI，未进行额外公开网页检索。没有派发子代理、修改产品源码/依赖/CI/持久环境、commit、push 或公开发布。新增报告保持未提交。

收尾输入/工作树记录见 [local-final.json](../build/release-local/step-05-publish-false-20261024-01/evidence/local-final.json)；[SHA256SUMS](../build/release-local/step-05-publish-false-20261024-01/evidence/SHA256SUMS)覆盖本轮工具适配器、脚本、失败/成功证据、结果、交接输入和本报告，另由独立完整 artifact-SHA256SUMS 覆盖全部 513 产物文件。大型编译缓存不加入索引；未清理旧结果或缓存。
