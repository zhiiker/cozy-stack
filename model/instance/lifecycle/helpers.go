package lifecycle

import (
	"context"
	"os"
	"strings"

	"github.com/cozy/cozy-stack/model/app"
	"github.com/cozy/cozy-stack/model/instance"
	"github.com/cozy/cozy-stack/model/vfs"
	"github.com/cozy/cozy-stack/pkg/config/config"
	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/utils"
	multierror "github.com/hashicorp/go-multierror"
	"golang.org/x/net/idna"
	"golang.org/x/sync/errgroup"
)

func update(inst *instance.Instance) error {
	if err := inst.Update(); err != nil {
		inst.Logger().Errorf("Could not update: %s", err.Error())
		return err
	}
	return nil
}

func installApp(inst *instance.Instance, slug string) error {
	source := "registry://" + slug + "/stable"
	installer, err := app.NewInstaller(inst, app.Copier(consts.WebappType, inst), &app.InstallerOptions{
		Operation:  app.Install,
		Type:       consts.WebappType,
		SourceURL:  source,
		Slug:       slug,
		Registries: inst.Registries(),
	})
	if err != nil {
		return err
	}
	_, err = installer.RunSync()
	return err
}

// DefineViewsAndIndex can be used to ensure that the CouchDB views and indexes
// used by the stack are correctly set.
func DefineViewsAndIndex(inst *instance.Instance) error {
	g, _ := errgroup.WithContext(context.Background())
	couchdb.DefineIndexes(g, inst, couchdb.Indexes)
	couchdb.DefineViews(g, inst, couchdb.Views)
	if err := g.Wait(); err != nil {
		return err
	}
	inst.IndexViewsVersion = couchdb.IndexViewsVersion
	return nil
}

func createDefaultFilesTree(inst *instance.Instance) error {
	var errf error

	createDir := func(dir *vfs.DirDoc, err error) {
		if err != nil {
			errf = multierror.Append(errf, err)
			return
		}
		dir.CozyMetadata = vfs.NewCozyMetadata(inst.PageURL("/", nil))
		err = inst.VFS().CreateDir(dir)
		if err != nil && !os.IsExist(err) {
			errf = multierror.Append(errf, err)
		}
	}

	name := inst.Translate("Tree Administrative")
	createDir(vfs.NewDirDocWithPath(name, consts.RootDirID, "/", nil))

	// Check if we create the "Photos" folder and its subfolders. By default, we
	// are creating it, but some contexts may not want to create them.
	createPhotosFolder := true
	if ctxSettings, ok := inst.SettingsContext(); ok {
		if photosFolderParam, ok := ctxSettings["init_photos_folder"]; ok {
			createPhotosFolder = photosFolderParam.(bool)
		}
	}

	if createPhotosFolder {
		name = inst.Translate("Tree Photos")
		createDir(vfs.NewDirDocWithPath(name, consts.RootDirID, "/", nil))
	}

	return errf
}

func checkAliases(inst *instance.Instance, aliases []string) ([]string, error) {
	if len(aliases) == 0 {
		return nil, nil
	}
	aliases = utils.UniqueStrings(aliases)
	kept := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		alias, err := validateDomain(alias)
		if err != nil {
			return nil, err
		}
		if alias == inst.Domain {
			return nil, instance.ErrExists
		}
		other, err := instance.GetFromCouch(alias)
		if err != instance.ErrNotFound {
			if err != nil {
				return nil, err
			}
			if other.ID() != inst.ID() {
				return nil, instance.ErrExists
			}
		}
		kept = append(kept, alias)
	}
	return kept, nil
}

const illegalChars = " /,;&?#@|='\"\t\r\n\x00"
const illegalFirstChars = "0123456789."

func validateDomain(domain string) (string, error) {
	var err error
	if domain, err = idna.ToUnicode(domain); err != nil {
		return "", instance.ErrIllegalDomain
	}
	domain = strings.TrimSpace(domain)
	if domain == "" || domain == ".." || domain == "." {
		return "", instance.ErrIllegalDomain
	}
	if strings.ContainsAny(domain, illegalChars) {
		return "", instance.ErrIllegalDomain
	}
	if strings.ContainsAny(domain[:1], illegalFirstChars) {
		return "", instance.ErrIllegalDomain
	}
	domain = strings.ToLower(domain)
	if config.GetConfig().Subdomains == config.FlatSubdomains {
		parts := strings.SplitN(domain, ".", 2)
		if strings.Contains(parts[0], "-") {
			return "", instance.ErrIllegalDomain
		}
	}
	return domain, nil
}
