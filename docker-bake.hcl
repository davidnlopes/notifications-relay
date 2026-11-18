# VERSION is set by the release workflow. Without it the image gets no tags, as on pull requests.
variable "VERSION" {
  default = ""
}

# GitHub Actions sets this on every runner; the default only applies to local builds.
variable "GITHUB_REPOSITORY" {
  default = "local/notifications-relay"
}

variable "GITHUB_SHA" {
  default = ""
}

variable "IMAGE" {
  default = "ghcr.io/${lower(GITHUB_REPOSITORY)}"
}

# Padding keeps the indexes valid when VERSION is empty.
variable "VERSION_PARTS" {
  default = split(".", "${VERSION}.0.0")
}

target "default" {
  context    = "."
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64", "linux/arm64"]
  tags = VERSION == "" ? [] : [
    "${IMAGE}:v${VERSION}",
    "${IMAGE}:v${VERSION_PARTS[0]}.${VERSION_PARTS[1]}",
    "${IMAGE}:v${VERSION_PARTS[0]}",
    "${IMAGE}:latest",
  ]
  labels = {
    "org.opencontainers.image.created"     = timestamp()
    "org.opencontainers.image.description" = "Small webhook relay: receive events from sources like GitHub, filter them with rules, and post them to chat channels like Discord."
    "org.opencontainers.image.licenses"    = "MIT"
    "org.opencontainers.image.revision"    = GITHUB_SHA
    "org.opencontainers.image.source"      = "https://github.com/${GITHUB_REPOSITORY}"
    "org.opencontainers.image.title"       = element(split("/", GITHUB_REPOSITORY), 1)
    "org.opencontainers.image.url"         = "https://github.com/${GITHUB_REPOSITORY}"
    "org.opencontainers.image.version"     = VERSION == "" ? "" : "v${VERSION}"
  }
}
