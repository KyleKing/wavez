# typed: false
# frozen_string_literal: true

class WhatDidAiDo < Formula
  desc "Quiz yourself on what your AI coding agent actually did, active-recall comprehension checks generated from real Claude Code and Aider session transcripts"
  homepage "https://github.com/kyleking/what-did-ai-do"
  license "MIT"
  version "0.1.0"

  on_macos do
    if Hardware::CPU.arm?
      url "#{homepage}/releases/download/v#{version}/what-did-ai-do-darwin-arm64"
      sha256 "REPLACE_WITH_SHA256_FOR_DARWIN_ARM64"
    else
      url "#{homepage}/releases/download/v#{version}/what-did-ai-do-darwin-amd64"
      sha256 "REPLACE_WITH_SHA256_FOR_DARWIN_AMD64"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "#{homepage}/releases/download/v#{version}/what-did-ai-do-linux-arm64"
      sha256 "REPLACE_WITH_SHA256_FOR_LINUX_ARM64"
    else
      url "#{homepage}/releases/download/v#{version}/what-did-ai-do-linux-amd64"
      sha256 "REPLACE_WITH_SHA256_FOR_LINUX_AMD64"
    end
  end

  def install
    binary_name = "what-did-ai-do-#{OS.kernel_name.downcase}-#{Hardware::CPU.arch}"
    bin.install binary_name => "what-did-ai-do"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/what-did-ai-do --version")
  end
end
