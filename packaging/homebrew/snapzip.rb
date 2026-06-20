class Snapzip < Formula
  desc "Local codebase memory and context packs for AI coding agents"
  homepage "https://github.com/MTEnt/SnapZip"
  url "https://github.com/MTEnt/SnapZip/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "d541ae58c92feb50a06dca7e32940a10afbb2d3be9e769e4b819742c82779f98"
  license "MIT"
  head "https://github.com/MTEnt/SnapZip.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = "-s -w -X main.version=v#{version} -X main.commit=e29a3d30e21c04e5116453245c552cbfacd3eac6 -X main.date=2026-06-20"
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/snapzip"
  end

  test do
    (testpath/"fixture.py").write <<~PY
      class CacheStore:
          pass
    PY

    system bin/"snapzip", "index", "--db-dir", testpath, "--langs", "python", "--crawl", testpath
    assert_match "knowledge rows: 1", shell_output("#{bin}/snapzip stats --db-dir #{testpath}")
    assert_match "snapzip v#{version}", shell_output("#{bin}/snapzip version")
  end
end
