class Snapzip < Formula
  desc "Local codebase memory and context packs for AI coding agents"
  homepage "https://github.com/MTEnt/SnapZip"
  license "MIT"
  head "https://github.com/MTEnt/SnapZip.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w"), "./cmd/snapzip"
  end

  test do
    (testpath/"fixture.py").write <<~PY
      class CacheStore:
          pass
    PY

    system bin/"snapzip", "index", "--db-dir", testpath, "--langs", "python", "--crawl", testpath
    assert_match "knowledge rows: 1", shell_output("#{bin}/snapzip stats --db-dir #{testpath}")
  end
end
