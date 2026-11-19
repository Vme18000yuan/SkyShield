"""Read official SkyShield releases and their matching Docker Hub images."""

import hashlib
import json
import re
import urllib.error
import urllib.parse
import urllib.request

REPOSITORY = "https://github.com/Vme18000yuan/SkyShield.git"
LATEST_RELEASE = "https://api.github.com/repos/Vme18000yuan/SkyShield/releases/latest"
COMPONENTS = ("server", "frontend", "bootstrap", "nginx")
IMAGE_ACCEPT = ", ".join((
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
))


class HTTPSRedirect(urllib.request.HTTPRedirectHandler):
    def __init__(self, host):
        self.host = host

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        url = urllib.parse.urlsplit(newurl)
        if url.scheme != "https" or (self.host and url.hostname != self.host):
            raise RuntimeError("发布源发生了不受支持的重定向。")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def fetch(url, *, accept, token=None, redirect_host=None):
    request = urllib.request.Request(url, headers={"Accept": accept, "User-Agent": "SkyShield-Update", "Cache-Control": "no-cache"})
    if token:
        request.add_unredirected_header("Authorization", "Bearer " + token)
    limit = 4 * 1024 * 1024
    opener = urllib.request.build_opener(HTTPSRedirect(redirect_host))
    with opener.open(request, timeout=25) as response:
        data = response.read(limit + 1)
    if len(data) > limit:
        raise RuntimeError("发布元数据过大。")
    return data


def latest_release_tag():
    try:
        release = json.loads(fetch(LATEST_RELEASE, accept="application/vnd.github+json", redirect_host="api.github.com"))
    except urllib.error.HTTPError as error:
        if error.code == 404:
            raise RuntimeError("GitHub 尚未发布 SkyShield 正式版本。") from error
        raise RuntimeError(f"GitHub 发布接口返回 HTTP {error.code}，请稍后重试。") from error
    except (OSError, ValueError) as error:
        raise RuntimeError("无法读取 GitHub 发布信息，请检查部署机网络和代理。") from error
    if not isinstance(release, dict) or release.get("draft") is not False or release.get("prerelease") is not False:
        raise RuntimeError("GitHub 未返回有效的正式版本。")
    tag = release.get("tag_name")
    if not isinstance(tag, str) or not re.fullmatch(r"v?[0-9][0-9A-Za-z._+-]{0,62}", tag):
        raise RuntimeError("正式版本标签无效。")
    return tag


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def image_metadata(base, resource, token, expected_digest):
    if not isinstance(expected_digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", expected_digest):
        raise RuntimeError("镜像元数据中的摘要无效。")
    data = fetch(f"{base}/{resource}/{expected_digest}", accept=IMAGE_ACCEPT, token=token)
    if digest(data) != expected_digest:
        raise RuntimeError("镜像元数据与摘要不匹配。")
    return json.loads(data)


def published_image(name, revision):
    repository = "solder110/skyshield-" + name
    tag = "sha-" + revision[:12]
    scope = urllib.parse.urlencode({"service": "registry.docker.io", "scope": f"repository:{repository}:pull"})
    base = f"https://registry-1.docker.io/v2/{repository}"
    try:
        token = json.loads(fetch(f"https://auth.docker.io/token?{scope}", accept="application/json"))["token"]
        raw = fetch(f"{base}/manifests/{tag}", accept=IMAGE_ACCEPT, token=token)
        manifest = json.loads(raw)
        if "manifests" in manifest:
            matches = [item for item in manifest["manifests"] if item.get("platform", {}).get("os") == "linux"
                       and item.get("platform", {}).get("architecture") == "amd64"]
            if len(matches) != 1:
                raise RuntimeError(f"{repository}:{tag} 缺少唯一的 Linux amd64 镜像。")
            manifest = image_metadata(base, "manifests", token, matches[0]["digest"])
        config = image_metadata(base, "blobs", token, manifest["config"]["digest"])
        labels = config.get("config", {}).get("Labels") or {}
        image_revision = labels.get("org.opencontainers.image.revision")
    except urllib.error.HTTPError as error:
        if error.code == 404:
            raise RuntimeError(f"Docker Hub 尚未提供配套镜像 {repository}:{tag}。请等镜像发布后重试。") from error
        raise RuntimeError(f"读取 Docker Hub 镜像 {repository}:{tag} 失败（HTTP {error.code}）。") from error
    except (OSError, ValueError, TypeError, KeyError, AttributeError) as error:
        raise RuntimeError(f"无法读取或解析 Docker Hub 镜像 {repository}:{tag} 的元数据。") from error
    if (config.get("os"), config.get("architecture")) != ("linux", "amd64") or image_revision != revision:
        raise RuntimeError(f"{repository}:{tag} 的平台或源码提交与正式版本不匹配。")
    return f"docker.io/{repository}@{digest(raw)}"


def published_images(revision):
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise RuntimeError("发布源码提交无效。")
    return {name: published_image(name, revision) for name in COMPONENTS}
