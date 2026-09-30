#!/bin/sh
# Instala o DevPulse no Linux ou no macOS:
#
#   curl -fsSL https://raw.githubusercontent.com/sidyjw/devpulse/main/install.sh | sh
#
# Baixa a release do seu sistema, confere o SHA256 com o SHA256SUMS.txt da
# release e roda `devpulse install`. Argumentos vão para o instalador:
#
#   curl -fsSL .../install.sh | sh -s -- --yes --harness claude --app code
#
# DEVPULSE_VERSION=0.2.0 fixa a versão (padrão: a mais recente).
set -eu

REPO="sidyjw/devpulse"
NAME="devpulse"

say() { printf '%s\n' "$*" >&2; }
die() { say "erro: $*"; exit 1; }

case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "sistema não suportado: $(uname -s) (no Windows, use o install.ps1)" ;;
esac
case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "arquitetura não suportada: $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"; }
	final_url() { curl -fsSLI --proto '=https' --tlsv1.2 -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q --https-only -O "$2" "$1"; }
	final_url() { wget -q --https-only --spider -S "$1" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -n 1 | tr -d '\r'; }
else
	die "instale o curl ou o wget"
fi

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	die "instale o sha256sum ou o shasum para conferir o download"
fi

for a in "$@"; do
	case "$a" in
		--no-copy | -no-copy) die "--no-copy não funciona aqui: o executável baixado é apagado no fim" ;;
	esac
done

version="${DEVPULSE_VERSION:-}"
if [ -z "$version" ]; then
	# releases/latest redireciona para .../releases/tag/vX.Y.Z: sem API, sem limite de requisições
	url="$(final_url "https://github.com/$REPO/releases/latest")" || die "não consegui consultar a última versão"
	version="${url##*/tag/}"
	[ "$version" != "$url" ] || die "não consegui descobrir a última versão ($url)"
fi
version="${version#v}"
case "$version" in
	*[!0-9A-Za-z.-]* | "") die "versão inválida: $version" ;;
esac

archive="${NAME}_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/v$version"
tmp="$(mktemp -d 2>/dev/null || mktemp -d -t devpulse)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Baixando $NAME $version ($os/$arch)..."
fetch "$base/$archive" "$tmp/$archive" || die "não consegui baixar $base/$archive"
fetch "$base/SHA256SUMS.txt" "$tmp/SHA256SUMS.txt" || die "não consegui baixar o SHA256SUMS.txt"

want="$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS.txt")"
[ -n "$want" ] || die "$archive não está no SHA256SUMS.txt"
got="$(sha256 "$tmp/$archive")"
[ "$got" = "$want" ] || die "o SHA256 de $archive não confere (esperado $want, obtido $got)"
say "✓ SHA256 conferido"
if command -v gh >/dev/null 2>&1; then
	say "  (opcional) para conferir a proveniência: gh attestation verify <arquivo> --repo $REPO"
fi

tar -xzf "$tmp/$archive" -C "$tmp"
bin="$tmp/${NAME}_${version}_${os}_${arch}/$NAME"
[ -x "$bin" ] || die "executável não encontrado em $archive"

# Em `curl | sh` a entrada é o próprio script: as perguntas vêm do terminal.
if [ -t 0 ]; then
	"$bin" install "$@"
elif [ -r /dev/tty ] && (: </dev/tty) 2>/dev/null; then
	"$bin" install "$@" </dev/tty
elif [ "$#" -gt 0 ]; then
	"$bin" install "$@"
else
	say "Sem terminal para as perguntas. Rode com as respostas em flags, por exemplo:"
	say "  curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh -s -- --yes --harness claude --app code ..."
	exit 1
fi
