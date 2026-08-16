# typed: false
# frozen_string_literal: true

class VoiceCli < Formula
  desc "CLI for importing and reviewing personal writing samples (iMessage, Mail, Linear) into a voice-guide corpus for Claude Code"
  homepage "https://github.com/kyleking/voice-cli"
  license "MIT"
  version "0.1.0"

  on_macos do
    if Hardware::CPU.arm?
      url "#{homepage}/releases/download/v#{version}/voice-cli-darwin-arm64"
      sha256 "REPLACE_WITH_SHA256_FOR_DARWIN_ARM64"
    else
      url "#{homepage}/releases/download/v#{version}/voice-cli-darwin-amd64"
      sha256 "REPLACE_WITH_SHA256_FOR_DARWIN_AMD64"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "#{homepage}/releases/download/v#{version}/voice-cli-linux-arm64"
      sha256 "REPLACE_WITH_SHA256_FOR_LINUX_ARM64"
    else
      url "#{homepage}/releases/download/v#{version}/voice-cli-linux-amd64"
      sha256 "REPLACE_WITH_SHA256_FOR_LINUX_AMD64"
    end
  end

  def install
    binary_name = "voice-cli-#{OS.kernel_name.downcase}-#{Hardware::CPU.arch}"
    bin.install binary_name => "voice-cli"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/voice-cli --version")
  end
end
