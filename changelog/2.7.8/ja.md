## 修正
- Docker Desktop が起動しているにもかかわらず、macOS で「Docker を利用できません」画面が表示される問題を修正しました。ランチャーは `docker` CLI と同じ方法で Docker を検出するようになり、アクティブな Docker コンテキスト（Docker Desktop、colima、Rancher Desktop）を参照し、必要に応じて既定のソケットパスだけでなく一般的な他のソケットの場所も確認します。
