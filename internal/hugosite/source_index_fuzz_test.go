package hugosite

import "testing"

func FuzzSlugFromRel(f *testing.F) {
	seeds := []string{
		"posts/demo/index.md",
		"posts/demo/index.fr.md",
		"posts/demo/index.en-US.md",
		"posts/demo.md",
		"posts/demo/index.txt",
		"posts//demo//index.md",
		"index.md",
		"",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rel string) {
		_ = SlugFromRel(rel)
	})
}
